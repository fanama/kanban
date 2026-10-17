package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"kanban-app/internal/domain"
)

// ReplaceBoard wipes the content of the board and writes the snapshot in, in
// one transaction: either the whole file lands or nothing changes. The cards
// are inserted before their constraint rows so the foreign keys always see the
// targets they point at.
func (r *Repo) ReplaceBoard(ctx context.Context, boardID string, columns []domain.Column, cards []domain.Card) error {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return err
	}
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		return replaceBoardTx(ctx, tx, boardID, columns, cards)
	})
	switch {
	case errors.Is(err, domain.ErrUnknownDependency), errors.Is(err, domain.ErrCardExists):
		return err
	case err != nil:
		return fmt.Errorf("sqlite: replace board %s: %w", boardID, err)
	}
	return nil
}

func replaceBoardTx(ctx context.Context, tx *sql.Tx, boardID string, columns []domain.Column, cards []domain.Card) error {
	if err := clearBoardTx(ctx, tx, boardID); err != nil {
		return err
	}
	if err := insertColumnsTx(ctx, tx, boardID, columns); err != nil {
		return err
	}
	if err := insertCardsTx(ctx, tx, boardID, cards); err != nil {
		return err
	}
	return insertCardDependenciesTx(ctx, tx, cards)
}

func clearBoardTx(ctx context.Context, tx *sql.Tx, boardID string) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM card_dependencies WHERE card_id IN (SELECT id FROM cards WHERE board_id = ?)`, boardID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cards WHERE board_id = ?`, boardID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM columns WHERE board_id = ?`, boardID)
	return err
}

func insertColumnsTx(ctx context.Context, tx *sql.Tx, boardID string, columns []domain.Column) error {
	for _, column := range domain.SortColumns(columns) {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO columns (id, name, position, board_id) VALUES (?, ?, ?, ?)`,
			column.ID, column.Name, column.Position, boardID); err != nil {
			return err
		}
	}
	return nil
}

func insertCardsTx(ctx context.Context, tx *sql.Tx, boardID string, cards []domain.Card) error {
	for _, card := range cards {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO cards (id, title, description, objective, status, priority, board_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			card.ID, card.Title, card.Description, card.Objective, card.ColumnID, string(card.Priority), boardID); err != nil {
			return err
		}
	}
	return nil
}

func insertCardDependenciesTx(ctx context.Context, tx *sql.Tx, cards []domain.Card) error {
	for _, card := range cards {
		if err := insertDependencies(ctx, tx, card.ID, card.DependsOn); err != nil {
			return err
		}
	}
	return nil
}
