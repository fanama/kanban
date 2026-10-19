package gsheets

import (
	"context"
	"fmt"

	"google.golang.org/api/sheets/v4"

	"kanban-app/internal/domain"
)

// ReplaceBoard rewrites the two tabs with the snapshot: the cards tab from A2
// down, padding the remnant of the old rows with blanks, and the columns tab
// the same way DeleteColumn renumbers it. The Sheets API has no transaction, so
// the write is best effort — the use case validated the snapshot first.
func (r *Repo) ReplaceBoard(ctx context.Context, boardID string, columns []domain.Column, cards []domain.Card) error {
	oldRows, err := r.readRows(ctx)
	if err != nil {
		return err
	}
	if err := r.replaceCards(ctx, oldRows, cards); err != nil {
		return err
	}

	oldColumnRows, err := r.readColumnRows(ctx)
	if err != nil {
		return err
	}
	return r.replaceColumns(ctx, oldColumnRows, columns)
}

func (r *Repo) replaceCards(ctx context.Context, oldRows [][]any, cards []domain.Card) error {
	values := make([][]any, 0, len(oldRows)+len(cards))
	for _, card := range cards {
		values = append(values, rowOf(card))
	}
	for len(values) < len(oldRows) {
		values = append(values, blankCardRow())
	}
	if len(values) == 0 {
		return nil
	}

	writeRange := fmt.Sprintf("%s!A2:H%d", r.sheet, len(values)+1)
	if _, err := r.svc.Spreadsheets.Values.
		Update(r.spreadsheet, writeRange, &sheets.ValueRange{Values: values}).
		ValueInputOption("RAW").
		Do(); err != nil {
		return fmt.Errorf("gsheets: rewrite the cards tab: %w", err)
	}
	return nil
}

func (r *Repo) replaceColumns(ctx context.Context, oldRows [][]any, columns []domain.Column) error {
	values := columnValues(domain.SortColumns(columns), len(oldRows))
	if len(values) == 0 {
		return nil
	}
	return r.writeColumnsRange(ctx, values)
}

func blankCardRow() []any {
	values := make([]any, len(header))
	for i := range values {
		values[i] = ""
	}
	return values
}
