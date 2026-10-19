package gsheets

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/api/sheets/v4"

	"kanban-app/internal/domain"
)

func (r *Repo) GetAll(ctx context.Context, boardID string) ([]domain.Card, error) {
	rows, err := r.readRows(ctx)
	if err != nil {
		return nil, err
	}

	cards := make([]domain.Card, 0, len(rows))
	for i, row := range rows {
		if cardID(row, i) == "" {
			continue // blank spacer row
		}
		cards = append(cards, cardFromRow(row, i))
	}
	return cards, nil
}

func (r *Repo) GetByID(ctx context.Context, boardID, id string) (domain.Card, error) {
	rows, err := r.readRows(ctx)
	if err != nil {
		return domain.Card{}, err
	}
	for i, row := range rows {
		if cardID(row, i) == id {
			return cardFromRow(row, i), nil
		}
	}
	return domain.Card{}, domain.ErrCardNotFound
}

func (r *Repo) Create(ctx context.Context, boardID string, card domain.Card) (domain.Card, error) {
	if _, err := r.svc.Spreadsheets.Values.
		Append(r.spreadsheet, r.dataRange, &sheets.ValueRange{Values: [][]any{rowOf(card)}}).
		ValueInputOption("RAW").
		InsertDataOption("INSERT_ROWS").
		Do(); err != nil {
		return domain.Card{}, fmt.Errorf("gsheets: append card: %w", err)
	}
	return card, nil
}

// Update rewrites every editable cell of the card's row.
func (r *Repo) Update(ctx context.Context, boardID string, card domain.Card) error {
	row, err := r.rowIndex(ctx, card.ID)
	if err != nil {
		return err
	}
	current, err := r.GetByID(ctx, boardID, card.ID)
	if err != nil {
		return err
	}
	if card.Version > 0 && current.Version != card.Version {
		return domain.ErrCardConflict
	}
	card.Version = current.Version + 1

	cellRange := fmt.Sprintf("%s!B%d:H%d", r.sheet, row, row)
	values := rowOf(card)[1:] // B..H: everything except the ID
	if _, err := r.svc.Spreadsheets.Values.
		Update(r.spreadsheet, cellRange, &sheets.ValueRange{Values: [][]any{values}}).
		ValueInputOption("RAW").
		Do(); err != nil {
		return fmt.Errorf("gsheets: update card %s: %w", card.ID, err)
	}
	return nil
}

// rowIndex returns the sheet row holding the card, 1-based with the header on
// row 1.
func (r *Repo) rowIndex(ctx context.Context, id string) (int, error) {
	rows, err := r.readRows(ctx)
	if err != nil {
		return 0, err
	}
	for i, row := range rows {
		if cardID(row, i) == id {
			return i + 2, nil
		}
	}
	return 0, domain.ErrCardNotFound
}

// rowOf flattens a card. The constraints go in one cell as a comma-separated
// list of card IDs, which is what a spreadsheet can hold.
func rowOf(card domain.Card) []any {
	if card.Version <= 0 {
		card.Version = 1
	}
	return []any{
		card.ID, card.Title, card.ColumnID, string(card.Priority),
		card.Description, card.Objective, strings.Join(card.DependsOn, ", "),
		fmt.Sprintf("%d", card.Version),
	}
}

// parseIDs reads the BlockedBy cell, tolerating commas, semicolons and spaces.
func parseIDs(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n'
	})

	var ids []string
	for _, f := range fields {
		if id := strings.TrimSpace(f); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// UpdateColumn writes the card's column ID in the status cell: moving a card
// only touches that one cell.
func (r *Repo) UpdateColumn(ctx context.Context, boardID, cardID, columnID string) error {
	row, err := r.rowIndex(ctx, cardID)
	if err != nil {
		return err
	}
	card, err := r.GetByID(ctx, boardID, cardID)
	if err != nil {
		return err
	}

	if err := r.writeCell(ctx, row, "C", columnID); err != nil {
		return fmt.Errorf("gsheets: move card %s: %w", cardID, err)
	}
	version := card.Version + 1
	if version < 1 {
		version = 1
	}
	if err := r.writeCell(ctx, row, "H", fmt.Sprintf("%d", version)); err != nil {
		return fmt.Errorf("gsheets: move card %s: %w", cardID, err)
	}
	return nil
}

func (r *Repo) writeCell(ctx context.Context, row int, column, value string) error {
	cellRange := fmt.Sprintf("%s!%s%d", r.sheet, column, row)
	_, err := r.svc.Spreadsheets.Values.
		Update(r.spreadsheet, cellRange, &sheets.ValueRange{Values: [][]any{{value}}}).
		ValueInputOption("RAW").
		Do()
	return err
}

// DeleteCard blanks the card's row, in place: the order of the remaining rows
// (what a human reads) is untouched, and a blank row is skipped by the reads.
func (r *Repo) DeleteCard(ctx context.Context, boardID, cardID string) error {
	row, err := r.rowIndex(ctx, cardID)
	if err != nil {
		return err
	}
	blank := make([]any, len(header))
	for i := range blank {
		blank[i] = ""
	}
	cellRange := fmt.Sprintf("%s!A%d:H%d", r.sheet, row, row)
	if _, err := r.svc.Spreadsheets.Values.
		Update(r.spreadsheet, cellRange, &sheets.ValueRange{Values: [][]any{blank}}).
		ValueInputOption("RAW").
		Do(); err != nil {
		return fmt.Errorf("gsheets: delete card %s: %w", cardID, err)
	}
	return nil
}

func (r *Repo) readRows(ctx context.Context) ([][]any, error) {
	res, err := r.svc.Spreadsheets.Values.Get(r.spreadsheet, r.dataRange).Do()
	if err != nil {
		return nil, fmt.Errorf("gsheets: read cards: %w", err)
	}
	return res.Values, nil
}
