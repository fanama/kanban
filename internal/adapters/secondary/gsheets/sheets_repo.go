// Package gsheets is a secondary adapter: it stores cards in a Google Sheet.
// The application core never imports this package; only cmd/server does.
package gsheets

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"

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

	svc, err := newSheetsService(ctx, cfg)
	if err != nil {
		return nil, err
	}
	sheetName, err := tabName(ctx, svc, cfg)
	if err != nil {
		return nil, err
	}

	repo := newRepo(svc, cfg.SpreadsheetID, sheetName)
	if err := repo.prepare(ctx); err != nil {
		return nil, err
	}
	return repo, nil
}

func newSheetsService(ctx context.Context, cfg Config) (*sheets.Service, error) {
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
	return svc, nil
}

func tabName(ctx context.Context, svc *sheets.Service, cfg Config) (string, error) {
	if cfg.SheetName != "" {
		return cfg.SheetName, nil
	}
	return resolveSheetName(ctx, svc, cfg)
}

func newRepo(svc *sheets.Service, spreadsheetID, sheetName string) *Repo {
	sheet := a1Name(sheetName)
	columns := a1Name(columnsSheet)
	return &Repo{
		svc:          svc,
		spreadsheet:  spreadsheetID,
		sheet:        sheet,
		dataRange:    fmt.Sprintf("%s!A2:G", sheet),
		headerRange:  fmt.Sprintf("%s!A1:G1", sheet),
		columnsRange: fmt.Sprintf("%s!A2:C", columns),
		columnsHead:  fmt.Sprintf("%s!A1:C1", columns),
	}
}

func (r *Repo) prepare(ctx context.Context) error {
	if err := r.ensureHeader(ctx); err != nil {
		return err
	}
	return r.ensureColumnsSheet(ctx)
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

// a1Name quotes a sheet name so ranges stay valid when the name has spaces.
func a1Name(name string) string {
	if strings.ContainsAny(name, " !'") {
		return "'" + strings.ReplaceAll(name, "'", "''") + "'"
	}
	return name
}
