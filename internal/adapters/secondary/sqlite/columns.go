package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	sqlite3 "github.com/mattn/go-sqlite3"

	"kanban-app/internal/domain"
)

func (r *Repo) GetColumns(ctx context.Context, boardID string) ([]domain.Column, error) {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, position FROM columns WHERE board_id = ? ORDER BY position`, boardID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: read columns: %w", err)
	}
	defer rows.Close()

	columns := make([]domain.Column, 0)
	for rows.Next() {
		var column domain.Column
		if err := rows.Scan(&column.ID, &column.Name, &column.Position); err != nil {
			return nil, fmt.Errorf("sqlite: scan column: %w", err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: read columns: %w", err)
	}
	return domain.SortColumns(columns), nil
}

func (r *Repo) CreateColumn(ctx context.Context, boardID string, column domain.Column) (domain.Column, error) {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return domain.Column{}, err
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO columns (id, name, position, board_id) VALUES (?, ?, ?, ?)`,
		column.ID, column.Name, column.Position, boardID)

	var serr sqlite3.Error
	if errors.As(err, &serr) && serr.Code == sqlite3.ErrConstraint {
		return domain.Column{}, domain.ErrColumnExists
	}
	if err != nil {
		return domain.Column{}, fmt.Errorf("sqlite: create column %s: %w", column.ID, err)
	}
	return column, nil
}

// RenameColumn replaces a column's name in place.
func (r *Repo) RenameColumn(ctx context.Context, boardID, id, name string) error {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE columns SET name = ? WHERE id = ? AND board_id = ?`, name, id, boardID)
	if err != nil {
		return fmt.Errorf("sqlite: rename column %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return domain.ErrColumnNotFound
	}
	return nil
}

func (r *Repo) DeleteColumn(ctx context.Context, boardID, id string) error {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return err
	}
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		return deleteColumnTx(ctx, tx, boardID, id)
	})
	switch {
	case errors.Is(err, domain.ErrColumnNotFound):
		return err
	case err != nil:
		return fmt.Errorf("sqlite: delete column %s: %w", id, err)
	}
	return nil
}

func deleteColumnTx(ctx context.Context, tx *sql.Tx, boardID, id string) error {
	// Read the position first: once the row is gone the gap can no longer be
	// located, and the surviving columns must keep a dense ordering.
	var position int
	err := tx.QueryRowContext(ctx,
		`SELECT position FROM columns WHERE id = ? AND board_id = ?`, id, boardID).Scan(&position)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return domain.ErrColumnNotFound
	case err != nil:
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM columns WHERE id = ? AND board_id = ?`, id, boardID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE columns SET position = position - 1 WHERE position > ? AND board_id = ?`, position, boardID)
	return err
}
