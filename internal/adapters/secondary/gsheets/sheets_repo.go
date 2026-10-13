// Package gsheets is a secondary adapter: it stores cards in a Google Sheet.
// The application core never imports this package; only cmd/server does.
package gsheets

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"

	"kanban-app/internal/domain"
	"kanban-app/internal/ports"
)

// header is the first row of the sheet; the column order must not change.
var header = []any{"ID", "Title", "Status", "Priority", "Description", "Objective", "BlockedBy"}

// The board's columns live in their own tab, so they can be created and deleted.
const columnsSheet = "Columns"

var columnsHeader = []any{"ID", "Name", "Position"}

// Config describes where the cards live.
type Config struct {
	SpreadsheetID string
	// SheetName pins a tab by title; SheetGID pins it by the gid in the sheet
	// URL. When both are empty the first tab of the spreadsheet is used.
	SheetName       string
	SheetGID        *int64
	CredentialsFile string // optional: falls back to Application Default Credentials
	Endpoint        string // optional: override the API endpoint (tests, emulators)
	NoAuth          bool   // optional: test-only, talks to the endpoint without credentials
}

// Repo is the Sheets-backed BoardRepository.
type Repo struct {
	svc          *sheets.Service
	spreadsheet  string
	sheet        string // quoted when needed, for A1 notation
	dataRange    string
	headerRange  string
	columnsRange string
	columnsHead  string
}

var _ ports.BoardRepository = (*Repo)(nil)

// NewSheetsRepo builds the adapter, resolves the tab to write to and makes sure
// it has a header row.
func NewSheetsRepo(ctx context.Context, cfg Config) (*Repo, error) {
	if cfg.SpreadsheetID == "" {
		return nil, fmt.Errorf("gsheets: spreadsheet ID is required")
	}

	opts := []option.ClientOption{option.WithScopes(sheets.SpreadsheetsScope)}
	if cfg.CredentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(cfg.CredentialsFile))
	}
	if cfg.Endpoint != "" {
		opts = append(opts, option.WithEndpoint(cfg.Endpoint))
	}
	if cfg.NoAuth {
		opts = append(opts, option.WithoutAuthentication())
	}

	svc, err := sheets.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("gsheets: create service: %w", err)
	}

	sheetName := cfg.SheetName
	if sheetName == "" {
		sheetName, err = resolveSheetName(ctx, svc, cfg)
		if err != nil {
			return nil, err
		}
	}

	sheet := a1Name(sheetName)
	columns := a1Name(columnsSheet)
	r := &Repo{
		svc:          svc,
		spreadsheet:  cfg.SpreadsheetID,
		sheet:        sheet,
		dataRange:    fmt.Sprintf("%s!A2:G", sheet),
		headerRange:  fmt.Sprintf("%s!A1:G1", sheet),
		columnsRange: fmt.Sprintf("%s!A2:C", columns),
		columnsHead:  fmt.Sprintf("%s!A1:C1", columns),
	}

	if err := r.ensureHeader(ctx); err != nil {
		return nil, err
	}
	if err := r.ensureColumnsSheet(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// ensureColumnsSheet creates the columns tab when the spreadsheet has none.
func (r *Repo) ensureColumnsSheet(ctx context.Context) error {
	_, err := r.svc.Spreadsheets.Values.Get(r.spreadsheet, r.columnsHead).Do()
	if err == nil {
		return nil
	}

	_, err = r.svc.Spreadsheets.BatchUpdate(r.spreadsheet, &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{{
			AddSheet: &sheets.AddSheetRequest{Properties: &sheets.SheetProperties{Title: columnsSheet}},
		}},
	}).Do()
	if err != nil {
		return fmt.Errorf("gsheets: create the %q tab: %w", columnsSheet, err)
	}

	if _, err := r.svc.Spreadsheets.Values.
		Update(r.spreadsheet, r.columnsHead, &sheets.ValueRange{Values: [][]any{columnsHeader}}).
		ValueInputOption("RAW").
		Do(); err != nil {
		return fmt.Errorf("gsheets: write the columns header: %w", err)
	}
	return nil
}

// SheetName is the tab title the repository is bound to.
func (r *Repo) SheetName() string { return strings.Trim(r.sheet, "'") }

// resolveSheetName asks the API which tab to use: the one matching SheetGID, or
// the first tab. This saves callers from having to know the tab title (which
// differs by locale, e.g. "Sheet1" vs "Feuille 1").
func resolveSheetName(ctx context.Context, svc *sheets.Service, cfg Config) (string, error) {
	res, err := svc.Spreadsheets.
		Get(cfg.SpreadsheetID).
		Fields("sheets.properties(sheetId,title,index)").
		Do()
	if err != nil {
		return "", fmt.Errorf("gsheets: read spreadsheet metadata (is the Sheets API enabled and the sheet shared with this account?): %w", err)
	}
	if len(res.Sheets) == 0 {
		return "", fmt.Errorf("gsheets: spreadsheet %s has no tabs", cfg.SpreadsheetID)
	}

	if cfg.SheetGID == nil {
		return res.Sheets[0].Properties.Title, nil
	}
	for _, s := range res.Sheets {
		if s.Properties != nil && s.Properties.SheetId == *cfg.SheetGID {
			return s.Properties.Title, nil
		}
	}
	return "", fmt.Errorf("gsheets: no tab with gid %d in spreadsheet %s", *cfg.SheetGID, cfg.SpreadsheetID)
}

func (r *Repo) ensureHeader(ctx context.Context) error {
	res, err := r.svc.Spreadsheets.Values.Get(r.spreadsheet, r.headerRange).Do()
	if err != nil {
		return fmt.Errorf("gsheets: read header: %w", err)
	}
	if len(res.Values) > 0 && len(res.Values[0]) > 0 {
		return nil
	}

	if _, err := r.svc.Spreadsheets.Values.
		Update(r.spreadsheet, r.headerRange, &sheets.ValueRange{Values: [][]any{header}}).
		ValueInputOption("RAW").
		Do(); err != nil {
		return fmt.Errorf("gsheets: write header: %w", err)
	}
	return nil
}

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

	cellRange := fmt.Sprintf("%s!B%d:G%d", r.sheet, row, row)
	values := rowOf(card)[1:] // B..G: everything except the ID
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
	return []any{
		card.ID, card.Title, card.ColumnID, string(card.Priority),
		card.Description, card.Objective, strings.Join(card.DependsOn, ", "),
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

	cellRange := fmt.Sprintf("%s!C%d", r.sheet, row)
	if _, err := r.svc.Spreadsheets.Values.
		Update(r.spreadsheet, cellRange, &sheets.ValueRange{Values: [][]any{{columnID}}}).
		ValueInputOption("RAW").
		Do(); err != nil {
		return fmt.Errorf("gsheets: move card %s: %w", cardID, err)
	}
	return nil
}

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

	kept := make([]domain.Column, 0, len(rows))
	survivors := make([]int, 0, len(rows))
	found := false
	for i, row := range rows {
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
		survivors = append(survivors, i)
	}
	if !found {
		return domain.ErrColumnNotFound
	}

	kept = domain.SortColumns(kept)
	values := make([][]any, 0, len(rows))
	for _, c := range kept {
		values = append(values, []any{c.ID, c.Name, c.Position})
	}
	for len(values) < len(rows) {
		values = append(values, []any{"", "", ""})
	}

	writeRange := fmt.Sprintf("%s!A2:C%d", a1Name(columnsSheet), len(values)+1)
	if _, err := r.svc.Spreadsheets.Values.
		Update(r.spreadsheet, writeRange, &sheets.ValueRange{Values: values}).
		ValueInputOption("RAW").
		Do(); err != nil {
		return fmt.Errorf("gsheets: rewrite the columns tab: %w", err)
	}
	return nil
}

// ReplaceBoard rewrites the two tabs with the snapshot: the cards tab from A2
// down, padding the remnant of the old rows with blanks, and the columns tab
// the same way DeleteColumn renumbers it. The Sheets API has no transaction, so
// the write is best effort — the use case validated the snapshot first.
func (r *Repo) ReplaceBoard(ctx context.Context, boardID string, columns []domain.Column, cards []domain.Card) error {
	oldRows, err := r.readRows(ctx)
	if err != nil {
		return err
	}

	values := make([][]any, 0, len(oldRows)+len(cards))
	for _, card := range cards {
		values = append(values, rowOf(card))
	}
	for len(values) < len(oldRows) {
		values = append(values, []any{"", "", "", "", "", "", ""})
	}
	if len(values) > 0 {
		writeRange := fmt.Sprintf("%s!A2:G%d", r.sheet, len(values)+1)
		if _, err := r.svc.Spreadsheets.Values.
			Update(r.spreadsheet, writeRange, &sheets.ValueRange{Values: values}).
			ValueInputOption("RAW").
			Do(); err != nil {
			return fmt.Errorf("gsheets: rewrite the cards tab: %w", err)
		}
	}

	oldColumnRows, err := r.readColumnRows(ctx)
	if err != nil {
		return err
	}
	columnValues := make([][]any, 0, len(oldColumnRows)+len(columns))
	for _, column := range domain.SortColumns(columns) {
		columnValues = append(columnValues, []any{column.ID, column.Name, column.Position})
	}
	for len(columnValues) < len(oldColumnRows) {
		columnValues = append(columnValues, []any{"", "", ""})
	}
	if len(columnValues) > 0 {
		writeRange := fmt.Sprintf("%s!A2:C%d", a1Name(columnsSheet), len(columnValues)+1)
		if _, err := r.svc.Spreadsheets.Values.
			Update(r.spreadsheet, writeRange, &sheets.ValueRange{Values: columnValues}).
			ValueInputOption("RAW").
			Do(); err != nil {
			return fmt.Errorf("gsheets: rewrite the columns tab: %w", err)
		}
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
	cellRange := fmt.Sprintf("%s!A%d:G%d", r.sheet, row, row)
	if _, err := r.svc.Spreadsheets.Values.
		Update(r.spreadsheet, cellRange, &sheets.ValueRange{Values: [][]any{blank}}).
		ValueInputOption("RAW").
		Do(); err != nil {
		return fmt.Errorf("gsheets: delete card %s: %w", cardID, err)
	}
	return nil
}

// DeleteBoard refuses: a spreadsheet holds exactly one board; bring another
// one if you want more.
func (r *Repo) DeleteBoard(ctx context.Context, id string) error {
	_ = ctx
	_ = id
	return domain.ErrBoardsUnsupported
}

// CreateBoard refuses: a spreadsheet holds exactly one board; the tab you
// pointed the storage at is it.
func (r *Repo) CreateBoard(ctx context.Context, board domain.Board) (domain.Board, error) {
	_ = ctx
	_ = board
	return domain.Board{}, domain.ErrBoardsUnsupported
}

// ListBoards reports the one board the tab holds. The board keeps the tab title
// as ID (stable: it is what the range notation already quotes) and as name —
// which is what a human reading the tab calls it too.
func (r *Repo) ListBoards(ctx context.Context) ([]domain.Board, error) {
	_ = ctx
	name := strings.Trim(r.sheet, "'")
	return []domain.Board{{ID: name, Name: name}}, nil
}

func (r *Repo) readRows(ctx context.Context) ([][]any, error) {
	res, err := r.svc.Spreadsheets.Values.Get(r.spreadsheet, r.dataRange).Do()
	if err != nil {
		return nil, fmt.Errorf("gsheets: read cards: %w", err)
	}
	return res.Values, nil
}

// cardFromRow maps a sheet row to a domain card. Rows written by hand may leave
// the ID column empty, in which case the row number becomes the ID. Missing
// trailing cells (a sheet still using the 4-column layout) read as empty, and a
// card whose column is unknown is placed by the use case's GetBoard fallback.
func cardFromRow(row []any, index int) domain.Card {
	return domain.Card{
		ID:          cardID(row, index),
		Title:       cell(row, 1),
		Description: cell(row, 4),
		Objective:   cell(row, 5),
		DependsOn:   parseIDs(cell(row, 6)),
		ColumnID:    cell(row, 2),
		Priority:    domain.Priority(cell(row, 3)).Normalize(),
	}
}

func cardID(row []any, index int) string {
	if id := cell(row, 0); id != "" {
		return id
	}
	if cell(row, 1) == "" && cell(row, 2) == "" && cell(row, 3) == "" {
		return "" // fully blank row
	}
	return fmt.Sprintf("row_%d", index+2)
}

// a1Name quotes a sheet name so ranges stay valid when the name has spaces.
func a1Name(name string) string {
	if strings.ContainsAny(name, " !'") {
		return "'" + strings.ReplaceAll(name, "'", "''") + "'"
	}
	return name
}

func cell(row []any, i int) string {
	if i >= len(row) {
		return ""
	}
	if s, ok := row[i].(string); ok {
		return strings.TrimSpace(s)
	}
	return fmt.Sprintf("%v", row[i])
}
