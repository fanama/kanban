package gsheets

import (
	"context"
	"fmt"
	"strconv"

	"google.golang.org/api/sheets/v4"

	"kanban-app/internal/domain"
)

func (r *Repo) GetColumns(ctx context.Context, boardID string) ([]domain.Column, error) {
	rows, err := r.readColumnRows(ctx)
	if err != nil {
		return nil, err
	}

	columns := make([]domain.Column, 0, len(rows))
	for _, row := range rows {
		id := cell(row, 0)
		if id == "" {
			continue // deleted column, blanked out
		}
		position, err := strconv.Atoi(cell(row, 2))
		if err != nil {
			position = len(columns)
		}
		columns = append(columns, domain.Column{ID: id, Name: cell(row, 1), Position: position})
	}
	return domain.SortColumns(columns), nil
}

func (r *Repo) CreateColumn(ctx context.Context, boardID string, column domain.Column) (domain.Column, error) {
	existing, err := r.GetColumns(ctx, boardID)
	if err != nil {
		return domain.Column{}, err
	}
	for _, c := range existing {
		if c.ID == column.ID {
			return domain.Column{}, domain.ErrColumnExists
		}
	}

	column.Position = len(existing)
	if _, err := r.svc.Spreadsheets.Values.
		Append(r.spreadsheet, r.columnsRange, &sheets.ValueRange{
			Values: [][]any{{column.ID, column.Name, column.Position}},
		}).
		ValueInputOption("RAW").
		InsertDataOption("INSERT_ROWS").
		Do(); err != nil {
		return domain.Column{}, fmt.Errorf("gsheets: append column: %w", err)
	}
	return column, nil
}

// RenameColumn replaces a column's name: it touches only the name cell of
// that column's row.
func (r *Repo) RenameColumn(ctx context.Context, boardID, id, name string) error {
	rows, err := r.readColumnRows(ctx)
	if err != nil {
		return err
	}
	for i, row := range rows {
		if cell(row, 0) != id {
			continue
		}
		cellRange := fmt.Sprintf("%s!B%d", a1Name(columnsSheet), i+2)
		if _, err := r.svc.Spreadsheets.Values.
			Update(r.spreadsheet, cellRange, &sheets.ValueRange{Values: [][]any{{name}}}).
			ValueInputOption("RAW").
			Do(); err != nil {
			return fmt.Errorf("gsheets: rename column %s: %w", id, err)
		}
		return nil
	}
	return domain.ErrColumnNotFound
}

// DeleteColumn blanks the row of the deleted column, renumbers the survivors and
// writes the whole range back, so the tab never keeps a stale row.
func (r *Repo) DeleteColumn(ctx context.Context, boardID, id string) error {
	rows, err := r.readColumnRows(ctx)
	if err != nil {
		return err
	}
	kept, found := survivingColumns(rows, id)
	if !found {
		return domain.ErrColumnNotFound
	}
	values := columnValues(kept, len(rows))
	return r.writeColumnsRange(ctx, values)
}

func survivingColumns(rows [][]any, id string) ([]domain.Column, bool) {
	kept := make([]domain.Column, 0, len(rows))
	found := false
	for _, row := range rows {
		rowID := cell(row, 0)
		if rowID == "" {
			continue
		}
		if rowID == id {
			found = true
			continue
		}
		position, err := strconv.Atoi(cell(row, 2))
		if err != nil {
			position = len(kept)
		}
		kept = append(kept, domain.Column{ID: rowID, Name: cell(row, 1), Position: position})
	}
	return domain.SortColumns(kept), found
}

// columnValues renders rounded columns and pads the remnant of the old rows
// with blanks so the tab never keeps a stale row.
func columnValues(columns []domain.Column, rowCount int) [][]any {
	values := make([][]any, 0, rowCount)
	for _, column := range columns {
		values = append(values, []any{column.ID, column.Name, column.Position})
	}
	for len(values) < rowCount {
		values = append(values, []any{"", "", ""})
	}
	return values
}

func (r *Repo) writeColumnsRange(ctx context.Context, values [][]any) error {
	writeRange := fmt.Sprintf("%s!A2:C%d", a1Name(columnsSheet), len(values)+1)
	if _, err := r.svc.Spreadsheets.Values.
		Update(r.spreadsheet, writeRange, &sheets.ValueRange{Values: values}).
		ValueInputOption("RAW").
		Do(); err != nil {
		return fmt.Errorf("gsheets: rewrite the columns tab: %w", err)
	}
	return nil
}

func (r *Repo) readColumnRows(ctx context.Context) ([][]any, error) {
	res, err := r.svc.Spreadsheets.Values.Get(r.spreadsheet, r.columnsRange).Do()
	if err != nil {
		return nil, fmt.Errorf("gsheets: read columns: %w", err)
	}
	return res.Values, nil
}
