package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	sqlite3 "github.com/mattn/go-sqlite3"

	"kanban-app/internal/domain"
)

func (r *Repo) ListBoards(ctx context.Context) ([]domain.Board, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name FROM boards ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: read boards: %w", err)
	}
	defer rows.Close()

	boards := make([]domain.Board, 0)
	for rows.Next() {
		var board domain.Board
		if err := rows.Scan(&board.ID, &board.Name); err != nil {
			return nil, fmt.Errorf("sqlite: scan board: %w", err)
		}
		boards = append(boards, board)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: read boards: %w", err)
	}
	return boards, nil
}

func (r *Repo) CreateBoard(ctx context.Context, board domain.Board) (domain.Board, error) {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO boards (id, name) VALUES (?, ?)`, board.ID, board.Name)

	var serr sqlite3.Error
	if errors.As(err, &serr) && serr.Code == sqlite3.ErrConstraint {
		return domain.Board{}, domain.ErrBoardExists
	}
	if err != nil {
		return domain.Board{}, fmt.Errorf("sqlite: create board %s: %w", board.ID, err)
	}
	return board, nil
}

// DeleteBoard drops the workspace and everything it holds in one transaction:
// either the board vanishes entirely or it is untouched. The foreign keys take
// the dependency rows with the cards. An unknown board is an error.
func (r *Repo) DeleteBoard(ctx context.Context, id string) error {
	if err := r.requireBoard(ctx, id); err != nil {
		return err
	}
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		return deleteBoardTx(ctx, tx, id)
	})
	switch {
	case errors.Is(err, domain.ErrCardExists), errors.Is(err, domain.ErrUnknownDependency):
		return err
	case err != nil:
		return fmt.Errorf("sqlite: delete board %s: %w", id, err)
	}
	return nil
}

func deleteBoardTx(ctx context.Context, tx *sql.Tx, id string) error {
	// The dependency rows and the cards go together through the foreign keys;
	// the columns and the workspace row are explicit.
	if _, err := tx.ExecContext(ctx, `DELETE FROM cards WHERE board_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM columns WHERE board_id = ?`, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM boards WHERE id = ?`, id)
	return err
}

// boardExists reports whether the board row is there, so a storage-backed
// method can answer an unknown board the same way an unknown column is answered.
func (r *Repo) boardExists(ctx context.Context, boardID string) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx, `SELECT 1 FROM boards WHERE id = ?`, boardID).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("sqlite: read board %s: %w", boardID, err)
	}
	return true, nil
}

// requireBoard maps an unknown board to ErrBoardNotFound, like it is done at the
// use case boundary.
func (r *Repo) requireBoard(ctx context.Context, boardID string) error {
	ok, err := r.boardExists(ctx, boardID)
	if err != nil || ok {
		return err
	}
	return domain.ErrBoardNotFound
}
