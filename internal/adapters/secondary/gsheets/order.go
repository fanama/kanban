package gsheets

import (
	"context"
	"fmt"

	"google.golang.org/api/sheets/v4"

	"kanban-app/internal/domain"
)

// MoveCardBefore rewrites the card rows with the moved row directly before the
// target row. A spreadsheet has no hidden rank cell, so the visible row order
// itself is the card order.
func (r *Repo) MoveCardBefore(ctx context.Context, boardID, cardID, beforeCardID string) error {
	rows, err := r.readRows(ctx)
	if err != nil {
		return err
	}
	from := rowOfCard(rows, cardID)
	if from < 0 {
		return domain.ErrCardNotFound
	}
	if beforeCardID == "" {
		return r.writeCardRows(ctx, appendRow(rows, from))
	}
	to := rowOfCard(rows, beforeCardID)
	if to < 0 {
		return domain.ErrCardNotFound
	}
	card, before := cardFromRow(rows[from], from), cardFromRow(rows[to], to)
	if card.ColumnID != before.ColumnID {
		return domain.ErrNotSameColumn
	}
	if from == to {
		return nil
	}

	reordered := make([][]any, 0, len(rows))
	for i, row := range rows {
		if i == from {
			continue
		}
		if i == to {
			reordered = append(reordered, padCardRow(rows[from]))
		}
		reordered = append(reordered, padCardRow(row))
	}
	return r.writeCardRows(ctx, reordered)
}

func rowOfCard(rows [][]any, id string) int {
	for i, row := range rows {
		if cardID(row, i) == id {
			return i
		}
	}
	return -1
}

func (r *Repo) writeCardRows(ctx context.Context, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	writeRange := fmt.Sprintf("%s!A2:H%d", r.sheet, len(rows)+1)
	if _, err := r.svc.Spreadsheets.Values.
		Update(r.spreadsheet, writeRange, &sheets.ValueRange{Values: rows}).
		ValueInputOption("RAW").
		Do(); err != nil {
		return fmt.Errorf("gsheets: reorder cards: %w", err)
	}
	return nil
}

func appendRow(rows [][]any, from int) [][]any {
	result := make([][]any, 0, len(rows))
	for i, row := range rows {
		if i != from {
			result = append(result, padCardRow(row))
		}
	}
	return append(result, padCardRow(rows[from]))
}

func padCardRow(row []any) []any {
	padded := make([]any, len(header))
	for i := range padded {
		if i < len(row) {
			padded[i] = row[i]
		}
	}
	return padded
}
