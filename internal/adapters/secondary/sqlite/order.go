package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"kanban-app/internal/domain"
)

// nextPositionTx returns the position that appends a card at the end of its
// column. Position is only meaningful together with the column ID.
func nextPositionTx(ctx context.Context, tx *sql.Tx, boardID, columnID string) (int, error) {
	var position int
	err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position), -1) + 1 FROM cards WHERE board_id = ? AND status = ?`,
		boardID, columnID).Scan(&position)
	return position, err
}

// MoveCardBefore renumbers the cards of one column so cardID sits immediately
// before beforeCardID. Other columns keep their positions.
func (r *Repo) MoveCardBefore(ctx context.Context, boardID, cardID, beforeCardID string) error {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return err
	}
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		return moveBeforeTx(ctx, tx, boardID, cardID, beforeCardID)
	})
	if errors.Is(err, domain.ErrCardNotFound) || errors.Is(err, domain.ErrNotSameColumn) {
		return err
	}
	if err != nil {
		return fmt.Errorf("sqlite: reorder card %s: %w", cardID, err)
	}
	return nil
}

func moveBeforeTx(ctx context.Context, tx *sql.Tx, boardID, cardID, beforeCardID string) error {
	columnID, err := cardColumnTx(ctx, tx, boardID, cardID)
	if err != nil {
		return err
	}
	ids, err := columnCardIDsTx(ctx, tx, boardID, columnID)
	if err != nil {
		return err
	}
	if beforeCardID == "" {
		return writePositionsTx(ctx, tx, boardID, appendLast(ids, cardID))
	}
	beforeColumn, err := cardColumnTx(ctx, tx, boardID, beforeCardID)
	if err != nil {
		return err
	}
	if columnID != beforeColumn {
		return domain.ErrNotSameColumn
	}
	if cardID == beforeCardID {
		return nil
	}
	reordered, ok := insertBefore(ids, cardID, beforeCardID)
	if !ok {
		return domain.ErrCardNotFound
	}
	return writePositionsTx(ctx, tx, boardID, reordered)
}

func writePositionsTx(ctx context.Context, tx *sql.Tx, boardID string, ids []string) error {
	for position, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE cards SET position = ? WHERE id = ? AND board_id = ?`,
			position, id, boardID); err != nil {
			return err
		}
	}
	return nil
}

func appendLast(ids []string, cardID string) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != cardID {
			result = append(result, id)
		}
	}
	return append(result, cardID)
}

func cardColumnTx(ctx context.Context, tx *sql.Tx, boardID, cardID string) (string, error) {
	var columnID string
	err := tx.QueryRowContext(ctx,
		`SELECT status FROM cards WHERE id = ? AND board_id = ?`, cardID, boardID).Scan(&columnID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrCardNotFound
	}
	return columnID, err
}

func columnCardIDsTx(ctx context.Context, tx *sql.Tx, boardID, columnID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM cards WHERE board_id = ? AND status = ? ORDER BY position, rowid`,
		boardID, columnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func insertBefore(ids []string, cardID, beforeCardID string) ([]string, bool) {
	from, to := -1, -1
	for i, id := range ids {
		switch id {
		case cardID:
			from = i
		case beforeCardID:
			to = i
		}
	}
	if from < 0 || to < 0 || from == to {
		return ids, from >= 0 && to >= 0
	}
	without := append(append([]string(nil), ids[:from]...), ids[from+1:]...)
	to = -1
	for i, id := range without {
		if id == beforeCardID {
			to = i
			break
		}
	}
	result := make([]string, 0, len(ids))
	result = append(result, without[:to]...)
	result = append(result, cardID)
	return append(result, without[to:]...), true
}
