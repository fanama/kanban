package gsheets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"kanban-app/internal/domain"
)

// fakeSheets is a stand-in for the Sheets API: it answers the calls the adapter
// makes (spreadsheet metadata, add a tab, read values, update values, append
// values) over two tabs, the cards and the columns.
type fakeSheets struct {
	mu sync.Mutex

	tabs             []tab
	headerRow        []any
	rows             [][]any
	columnsHeaderRow []any
	columnRows       [][]any

	updates      []valueCall
	appends      []valueCall
	batchUpdates int
	lastRange    string
}

type tab struct {
	SheetID int64  `json:"sheetId"`
	Title   string `json:"title"`
	Index   int64  `json:"index"`
}

type valueCall struct {
	Range  string
	Values [][]any
}

func (f *fakeSheets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")

	if strings.HasSuffix(r.URL.Path, ":batchUpdate") {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Requests []struct {
				AddSheet struct {
					Properties struct {
						Title string `json:"title"`
					} `json:"properties"`
				} `json:"addSheet"`
			} `json:"requests"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for _, request := range req.Requests {
			title := request.AddSheet.Properties.Title
			if title == "" {
				continue
			}
			f.batchUpdates++
			f.tabs = append(f.tabs, tab{SheetID: int64(100 + len(f.tabs)), Title: title, Index: int64(len(f.tabs))})
		}
		json.NewEncoder(w).Encode(map[string]any{"spreadsheetId": "sheet-123"})
		return
	}

	idx := strings.Index(r.URL.Path, "/values/")
	if idx < 0 {
		// Spreadsheet metadata: /v4/spreadsheets/{id}?fields=sheets.properties...
		sheets := make([]map[string]any, 0, len(f.tabs))
		for _, t := range f.tabs {
			sheets = append(sheets, map[string]any{"properties": t})
		}
		json.NewEncoder(w).Encode(map[string]any{"sheets": sheets})
		return
	}

	isAppend := strings.Contains(r.URL.Path, ":append")
	rng := strings.TrimSuffix(r.URL.Path[idx+len("/values/"):], ":append")
	f.lastRange = rng

	name, _ := splitRange(rng)
	if !f.hasTab(name) {
		// What the real API answers for a range in a tab that does not exist.
		http.Error(w, `{"error":{"message":"Unable to parse range: `+rng+`"}}`, http.StatusBadRequest)
		return
	}
	isColumns := name == columnsSheet

	switch {
	case r.Method == http.MethodGet:
		values := f.rows
		header := f.headerRow
		if isColumns {
			values, header = f.columnRows, f.columnsHeaderRow
		}
		if strings.Contains(rng, "!A1:") {
			values = nil
			if header != nil {
				values = [][]any{header}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"range": rng, "values": values})

	case r.Method == http.MethodPut || r.Method == http.MethodPost:
		body, _ := io.ReadAll(r.Body)
		var vr struct{ Values [][]any }
		if err := json.Unmarshal(body, &vr); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(vr.Values) == 0 {
			http.Error(w, "empty values", http.StatusBadRequest)
			return
		}
		call := valueCall{Range: rng, Values: vr.Values}

		if isAppend {
			f.appends = append(f.appends, call)
			if isColumns {
				f.columnRows = append(f.columnRows, vr.Values[0])
			} else {
				f.rows = append(f.rows, vr.Values[0])
			}
			json.NewEncoder(w).Encode(map[string]any{"updates": map[string]any{"updatedRows": 1}})
			return
		}

		f.updates = append(f.updates, call)
		switch {
		case isColumns && strings.Contains(rng, "!A1:"):
			f.columnsHeaderRow = vr.Values[0]
		case isColumns && strings.Contains(rng, "!A2:"):
			f.columnRows = vr.Values // the adapter rewrites the whole tab
		case isColumns:
			f.overlayColumnCell(rng, vr.Values) // a rename touches single cells
		case strings.Contains(rng, "!A1:"):
			f.headerRow = vr.Values[0]
		case !isColumns && startsAtRow(rng, 2):
			f.overlayRows(rng, vr.Values)
		}
		json.NewEncoder(w).Encode(map[string]any{"updatedCells": 1})

	default:
		http.Error(w, "unsupported", http.StatusMethodNotAllowed)
	}
}

// startsAtRow reports whether a values range begins at the given sheet row
// (row 1 is the header). Used to tell a block rewrite from a single-cell write.
func startsAtRow(rng string, row int) bool {
	_, rest := splitRange(rng)
	start := strings.Trim(strings.Split(rest, ":")[0], "A'$")
	n, err := strconv.Atoi(start)
	return err == nil && n == row
}

// overlayRows writes values where the range points inside the stored block,
// leaving the rows the range does not cover alone — a sheet has no transaction,
// but it does not erase rows below a write either.
func (f *fakeSheets) overlayRows(rng string, values [][]any) {
	_, rest := splitRange(rng)
	start := 2
	if n, err := strconv.Atoi(strings.Trim(strings.Split(rest, ":")[0], "A'$")); err == nil {
		start = n
	}
	offset := start - 2
	for len(f.rows) < offset+len(values) {
		f.rows = append(f.rows, []any{"", "", "", "", "", "", ""})
	}
	for i, row := range values {
		f.rows[offset+i] = row
	}
}

// overlayColumnCell writes a values block into the stored columns rows where
// the range points, leaving the cells it does not cover alone.
func (f *fakeSheets) overlayColumnCell(rng string, values [][]any) {
	_, rest := splitRange(rng)
	first := strings.Split(rest, ":")[0]
	i := 0
	for i < len(first) && first[i] >= 'A' && first[i] <= 'Z' {
		i++
	}
	col := int(first[0] - 'A') // the columns tab spans A..C
	row, err := strconv.Atoi(first[i:])
	if err != nil {
		return
	}
	offset := row - 2
	for len(f.columnRows) < offset+len(values) {
		f.columnRows = append(f.columnRows, []any{"", "", ""})
	}
	for r, rowVals := range values {
		out := f.columnRows[offset+r]
		for c, v := range rowVals {
			for len(out) <= col+c {
				out = append(out, "")
			}
			out[col+c] = v
		}
		f.columnRows[offset+r] = out
	}
}

// flatten renders written values as text: the fake API round-trips them through
// JSON, so a position read back as a number is a float64.
func flatten(values [][]any) [][]string {
	out := make([][]string, 0, len(values))
	for _, row := range values {
		cells := make([]string, 0, len(row))
		for _, cell := range row {
			cells = append(cells, fmt.Sprintf("%v", cell))
		}
		out = append(out, cells)
	}
	return out
}

// splitRange returns the tab name a range points at, unquoted, and the rest of
// the range.
func splitRange(rng string) (string, string) {
	tab, rest, found := strings.Cut(rng, "!")
	if !found {
		tab = rng
	}
	return strings.Trim(tab, "'"), rest
}

func (f *fakeSheets) hasTab(name string) bool {
	for _, t := range f.tabs {
		if t.Title == name {
			return true
		}
	}
	return false
}

// newFakeRepo spins up a fake Sheets API and returns an adapter pointed at it.
// The columns tab already exists, as it would on a spreadsheet in use.
func newFakeRepo(t *testing.T, cfg Config, rows [][]any, tabs ...tab) (*fakeSheets, *Repo) {
	t.Helper()

	if len(tabs) == 0 {
		tabs = []tab{{SheetID: 0, Title: "Sheet1", Index: 0}}
	}
	fake := &fakeSheets{
		rows:             rows,
		tabs:             append(tabs, tab{SheetID: 99, Title: columnsSheet, Index: int64(len(tabs))}),
		headerRow:        header,
		columnsHeaderRow: columnsHeader,
		columnRows: [][]any{
			{"col_a", "To Do", 0},
			{"col_b", "Doing", 1},
		},
	}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	cfg.SpreadsheetID = "sheet-123"
	cfg.Endpoint = srv.URL
	cfg.NoAuth = true

	repo, err := NewSheetsRepo(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewSheetsRepo failed: %v", err)
	}
	return fake, repo
}

func TestSheetsRepoMapsRows(t *testing.T) {
	rows := [][]any{
		{"card_a", "Alpha", "Doing", "High", "What it is", "What done looks like", "card_b, card_c"},
		{"card_b", "Beta", "Doing", "", "", "", ""},
		{"", "Written by hand", "To Do", "Low", "", "", ""},
		{"", "", "", ""},
	}
	_, repo := newFakeRepo(t, Config{SheetName: "Sheet1"}, rows)

	ctx := context.Background()
	cards, err := repo.GetAll(ctx, "tab")
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}
	if len(cards) != 3 {
		t.Fatalf("Expected 3 cards (blank row skipped), got %d: %v", len(cards), cards)
	}
	if cards[0].ID != "card_a" || cards[0].ColumnID != "Doing" || cards[0].Priority != domain.PriorityHigh {
		t.Errorf("First row mapped incorrectly: %+v", cards[0])
	}
	if cards[0].Description != "What it is" || cards[0].Objective != "What done looks like" {
		t.Errorf("Content columns mapped incorrectly: %+v", cards[0])
	}
	if !reflect.DeepEqual(cards[0].DependsOn, []string{"card_b", "card_c"}) {
		t.Errorf("Constraints mapped incorrectly: %v", cards[0].DependsOn)
	}
	if cards[1].Priority != domain.PriorityLow {
		t.Errorf("Empty priority should fall back to Low, got %q", cards[1].Priority)
	}
	if cards[2].ID != "row_4" || cards[2].Title != "Written by hand" {
		t.Errorf("A row without an ID should fall back to its row number: %+v", cards[2])
	}

	got, err := repo.GetByID(ctx, "tab", "card_b")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got.Title != "Beta" {
		t.Errorf("Expected Beta, got %q", got.Title)
	}

	if _, err := repo.GetByID(ctx, "tab", "missing"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound, got %v", err)
	}
}

// TestSheetsRepoMoveWritesOneCell covers the move path: only the column cell of
// the card's row changes.
func TestSheetsRepoMoveWritesOneCell(t *testing.T) {
	rows := [][]any{
		{"card_a", "Alpha", "To Do", "High"},
		{"card_b", "Beta", "To Do", "Low"},
	}
	fake, repo := newFakeRepo(t, Config{SheetName: "Sheet1"}, rows)

	if err := repo.UpdateColumn(context.Background(), "tab", "card_b", "Done"); err != nil {
		t.Fatalf("UpdateColumn failed: %v", err)
	}
	if len(fake.updates) != 1 {
		t.Fatalf("Expected one write, got %d", len(fake.updates))
	}
	if fake.updates[0].Range != "Sheet1!C3" {
		t.Errorf("Expected the column cell of row 3, got %q", fake.updates[0].Range)
	}
	if got := fake.updates[0].Values[0][0]; got != "Done" {
		t.Errorf("Expected the new column to be written, got %v", got)
	}

	if err := repo.UpdateColumn(context.Background(), "tab", "missing", "Done"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound, got %v", err)
	}
}

func TestSheetsRepoResolvesFirstTabWhenUnset(t *testing.T) {
	tabs := []tab{
		{SheetID: 0, Title: "Feuille 1", Index: 0},
		{SheetID: 123456, Title: "Archive", Index: 1},
	}
	fake, repo := newFakeRepo(t, Config{}, [][]any{{"card_a", "Alpha", "To Do", "Low"}}, tabs...)

	if repo.SheetName() != "Feuille 1" {
		t.Errorf("Expected the first tab to be used, got %q", repo.SheetName())
	}
	if _, err := repo.GetAll(context.Background(), "tab"); err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}
	if fake.lastRange != "'Feuille 1'!A2:G" {
		t.Errorf("Expected a quoted range for the resolved tab, got %q", fake.lastRange)
	}
}

func TestSheetsRepoResolvesTabByGID(t *testing.T) {
	tabs := []tab{
		{SheetID: 0, Title: "Feuille 1", Index: 0},
		{SheetID: 123456, Title: "Archive", Index: 1},
	}
	gid := int64(123456)
	_, repo := newFakeRepo(t, Config{SheetGID: &gid}, nil, tabs...)

	if repo.SheetName() != "Archive" {
		t.Errorf("Expected the gid tab to be used, got %q", repo.SheetName())
	}

	unknown := int64(999)
	if _, err := NewSheetsRepo(context.Background(), Config{
		SpreadsheetID: "sheet-123",
		SheetGID:      &unknown,
		Endpoint:      "http://127.0.0.1:1", // never reached: resolution fails first
		NoAuth:        true,
	}); err == nil {
		t.Error("Expected an error for an unknown gid")
	}
}

// TestSheetsRepoUpdatesAllCells covers the edit path: every editable column of
// the row is rewritten, and the ID column is left alone.
func TestSheetsRepoUpdatesAllCells(t *testing.T) {
	rows := [][]any{{"card_a", "Alpha", "To Do", "Low", "", "", ""}}
	fake, repo := newFakeRepo(t, Config{SheetName: "Sheet1"}, rows)

	edited := domain.Card{
		ID:          "card_a",
		Title:       "Alpha v2",
		Description: "Description",
		Objective:   "Objective",
		DependsOn:   []string{"card_b", "card_c"},
		ColumnID:    "Doing",
		Priority:    domain.PriorityHigh,
	}
	if err := repo.Update(context.Background(), "tab", edited); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	if len(fake.updates) != 1 {
		t.Fatalf("Expected one write, got %d: %+v", len(fake.updates), fake.updates)
	}
	if fake.updates[0].Range != "Sheet1!B2:G2" {
		t.Errorf("Expected the row's editable cells, got %q", fake.updates[0].Range)
	}
	want := []any{"Alpha v2", "Doing", "High", "Description", "Objective", "card_b, card_c"}
	if got := fake.updates[0].Values[0]; len(got) != len(want) {
		t.Fatalf("Expected %d cells, got %v", len(want), got)
	}
	for i := range want {
		if fake.updates[0].Values[0][i] != want[i] {
			t.Errorf("Cell %d: got %v, want %v", i, fake.updates[0].Values[0][i], want[i])
		}
	}

	if err := repo.Update(context.Background(), "tab", domain.Card{ID: "missing", Title: "Ghost", ColumnID: "To Do", Priority: domain.PriorityLow}); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound, got %v", err)
	}
}

// TestSheetsRepoPreparesAnEmptySpreadsheet checks the startup path on a brand
// new spreadsheet: both headers are written and the columns tab is created.
func TestSheetsRepoPreparesAnEmptySpreadsheet(t *testing.T) {
	fake := &fakeSheets{tabs: []tab{{SheetID: 0, Title: "Sheet1"}}}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	if _, err := NewSheetsRepo(context.Background(), Config{
		SpreadsheetID: "sheet-123",
		SheetName:     "Sheet1",
		Endpoint:      srv.URL,
		NoAuth:        true,
	}); err != nil {
		t.Fatalf("NewSheetsRepo failed: %v", err)
	}

	if fake.batchUpdates != 1 || !fake.hasTab(columnsSheet) {
		t.Fatalf("Expected the columns tab to be created, got %d batch updates", fake.batchUpdates)
	}
	if len(fake.updates) != 2 {
		t.Fatalf("Expected the two headers to be written, got %+v", fake.updates)
	}
	for _, update := range fake.updates {
		switch update.Range {
		case "Sheet1!A1:G1":
			// The order and the names are a contract with the spreadsheets
			// written by earlier versions: renaming a cell orphans its data.
			want := []string{"ID", "Title", "Status", "Priority", "Description", "Objective", "BlockedBy"}
			if got := flatten(update.Values); !reflect.DeepEqual(got[0], want) {
				t.Errorf("Card header is %v, want %v", got[0], want)
			}
		case "Columns!A1:C1":
			want := []string{"ID", "Name", "Position"}
			if got := flatten(update.Values); !reflect.DeepEqual(got[0], want) {
				t.Errorf("Columns header is %v, want %v", got[0], want)
			}
		default:
			t.Errorf("Unexpected write to %q", update.Range)
		}
	}
}

// TestSheetsRepoColumnsRoundTrip covers reading the columns tab, appending a new
// column and refusing a duplicate ID.
func TestSheetsRepoColumnsRoundTrip(t *testing.T) {
	fake, repo := newFakeRepo(t, Config{SheetName: "Sheet1"}, nil)

	ctx := context.Background()
	columns, err := repo.GetColumns(ctx, "tab")
	if err != nil {
		t.Fatalf("GetColumns failed: %v", err)
	}
	want := []domain.Column{
		{ID: "col_a", Name: "To Do", Position: 0},
		{ID: "col_b", Name: "Doing", Position: 1},
	}
	if !reflect.DeepEqual(columns, want) {
		t.Fatalf("GetColumns returned %+v, want %+v", columns, want)
	}

	created, err := repo.CreateColumn(ctx, "tab", domain.Column{ID: "col_c", Name: "Done", Position: 7})
	if err != nil {
		t.Fatalf("CreateColumn failed: %v", err)
	}
	if created.Position != 2 {
		t.Errorf("Expected the column appended after the existing ones, got position %d", created.Position)
	}
	if len(fake.appends) != 1 || fake.appends[0].Range != "Columns!A2:C" {
		t.Fatalf("Expected an append to the columns tab, got %+v", fake.appends)
	}
	if got := flatten(fake.appends[0].Values)[0]; len(got) != 3 || got[0] != "col_c" || got[1] != "Done" || got[2] != "2" {
		t.Errorf("Appended the wrong row: %v", got)
	}

	if _, err := repo.CreateColumn(ctx, "tab", domain.Column{ID: "col_a", Name: "Duplicate", Position: 3}); !errors.Is(err, domain.ErrColumnExists) {
		t.Errorf("Expected ErrColumnExists, got %v", err)
	}
}

// TestSheetsRepoDeleteColumn checks that the deleted column's row is blanked out
// and the survivors are renumbered.
func TestSheetsRepoDeleteColumn(t *testing.T) {
	fake, repo := newFakeRepo(t, Config{SheetName: "Sheet1"}, nil)
	fake.columnRows = [][]any{
		{"col_a", "To Do", 0},
		{"col_b", "Doing", 1},
		{"col_c", "Done", 2},
	}

	ctx := context.Background()
	if err := repo.DeleteColumn(ctx, "tab", "col_b"); err != nil {
		t.Fatalf("DeleteColumn failed: %v", err)
	}

	if len(fake.updates) != 1 {
		t.Fatalf("Expected one write, got %+v", fake.updates)
	}
	if fake.updates[0].Range != "Columns!A2:C4" {
		t.Errorf("Expected the whole columns tab to be rewritten, got %q", fake.updates[0].Range)
	}
	want := [][]string{
		{"col_a", "To Do", "0"},
		{"col_c", "Done", "1"},
		{"", "", ""},
	}
	if got := flatten(fake.updates[0].Values); !reflect.DeepEqual(got, want) {
		t.Errorf("Rewrote the columns tab as %v, want %v", got, want)
	}

	columns, err := repo.GetColumns(ctx, "tab")
	if err != nil {
		t.Fatalf("GetColumns failed: %v", err)
	}
	if !reflect.DeepEqual(columns, []domain.Column{{ID: "col_a", Name: "To Do", Position: 0}, {ID: "col_c", Name: "Done", Position: 1}}) {
		t.Errorf("Expected the survivors renumbered, got %+v", columns)
	}

	if err := repo.DeleteColumn(ctx, "tab", "missing"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected ErrColumnNotFound, got %v", err)
	}
}

// TestSheetsRepoRenameColumn replaces a column's name with a single-cell write:
// the rest of the row and the other rows are untouched.
func TestSheetsRepoRenameColumn(t *testing.T) {
	fake, repo := newFakeRepo(t, Config{SheetName: "Sheet1"}, nil)
	fake.columnRows = [][]any{
		{"col_a", "To Do", 0},
		{"col_b", "Doing", 1},
	}

	ctx := context.Background()
	if err := repo.RenameColumn(ctx, "tab", "col_b", "Work in progress"); err != nil {
		t.Fatalf("RenameColumn failed: %v", err)
	}

	if len(fake.updates) != 1 {
		t.Fatalf("Expected one write, got %+v", fake.updates)
	}
	if fake.updates[0].Range != "Columns!B3" {
		t.Errorf("Expected the name cell only, got %q", fake.updates[0].Range)
	}
	if got := flatten(fake.updates[0].Values); !reflect.DeepEqual(got, [][]string{{"Work in progress"}}) {
		t.Errorf("Wrote %v, want the new name alone", got)
	}

	columns, err := repo.GetColumns(ctx, "tab")
	if err != nil {
		t.Fatalf("GetColumns failed: %v", err)
	}
	want := []domain.Column{
		{ID: "col_a", Name: "To Do", Position: 0},
		{ID: "col_b", Name: "Work in progress", Position: 1},
	}
	if !reflect.DeepEqual(columns, want) {
		t.Errorf("Expected only the name changed, got %+v, want %+v", columns, want)
	}

	if err := repo.RenameColumn(ctx, "tab", "missing", "X"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected ErrColumnNotFound, got %v", err)
	}
}

// TestSheetsRepoParsesConstraintLists covers the BlockedBy cell: a spreadsheet
// holds the constraints as text, and people separate them however they like.
func TestSheetsRepoParsesConstraintLists(t *testing.T) {
	tests := []struct {
		name      string
		blockedBy string
		want      []string
	}{
		{name: "comma separated", blockedBy: "card_b, card_c", want: []string{"card_b", "card_c"}},
		{name: "semicolons", blockedBy: "card_b;card_c", want: []string{"card_b", "card_c"}},
		{name: "extra spaces", blockedBy: "  card_b ,  card_c  ", want: []string{"card_b", "card_c"}},
		{name: "empty", blockedBy: "", want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows := [][]any{{"card_a", "Alpha", "To Do", "Low", "", "", tc.blockedBy}}
			_, repo := newFakeRepo(t, Config{SheetName: "Sheet1"}, rows)

			cards, err := repo.GetAll(context.Background(), "tab")
			if err != nil {
				t.Fatalf("GetAll failed: %v", err)
			}
			if len(cards) != 1 {
				t.Fatalf("Expected one card, got %v", cards)
			}
			if !reflect.DeepEqual(cards[0].DependsOn, tc.want) {
				t.Errorf("Expected %v, got %v", tc.want, cards[0].DependsOn)
			}
		})
	}
}

// TestSheetsRepoReplaceBoard covers the import path: the two tabs are rewritten
// with the snapshot, the remnant of the old rows is blanked out, and reading
// the board back gives the imported content.
func TestSheetsRepoReplaceBoard(t *testing.T) {
	rows := [][]any{
		{"card_a", "Alpha", "To Do", "Low", "", "", ""},
		{"card_b", "Beta", "Doing", "Medium", "", "", ""},
	}
	fake, repo := newFakeRepo(t, Config{SheetName: "Sheet1"}, rows)
	fake.columnRows = [][]any{{"col_a", "To Do", 0}, {"col_b", "Doing", 1}, {"col_c", "Done", 2}}

	columns := []domain.Column{{ID: "Only", Name: "Only", Position: 0}}
	cards := []domain.Card{
		{ID: "imp_0", Title: "Imported", ColumnID: "Only", Priority: domain.PriorityHigh},
	}
	if err := repo.ReplaceBoard(context.Background(), "tab", columns, cards); err != nil {
		t.Fatalf("ReplaceBoard failed: %v", err)
	}

	cardWrites, columnWrites := 0, 0
	for _, update := range fake.updates {
		switch {
		case strings.HasPrefix(update.Range, "Sheet1!A2:"):
			cardWrites++
			if update.Range != "Sheet1!A2:G3" {
				t.Errorf("Expected the cards block of 2 rows (one blank padding row), got %q", update.Range)
			}
			want := [][]string{{"imp_0", "Imported", "Only", "High", "", "", ""}, {"", "", "", "", "", "", ""}}
			if got := flatten(update.Values); !reflect.DeepEqual(got, want) {
				t.Errorf("Cards tab rewritten as %v, want %v", got, want)
			}
		case strings.HasPrefix(update.Range, "Columns!A2:"):
			columnWrites++
			if update.Range != "Columns!A2:C4" {
				t.Errorf("Expected the columns block of 3 rows, got %q", update.Range)
			}
			want := [][]string{{"Only", "Only", "0"}, {"", "", ""}, {"", "", ""}}
			if got := flatten(update.Values); !reflect.DeepEqual(got, want) {
				t.Errorf("Columns tab rewritten as %v, want %v", got, want)
			}
		default:
			t.Errorf("Unexpected write to %q", update.Range)
		}
	}
	if cardWrites != 1 || columnWrites != 1 {
		t.Errorf("Expected one write per tab, got %d card writes and %d column writes", cardWrites, columnWrites)
	}
}

// TestSheetsRepoOneBoardPerSpreadsheet covers the two board rules of the
// adapter: the tab you pointed the storage at is the only board it lists, and
// creating or deleting a board there is refused.
func TestSheetsRepoOneBoardPerSpreadsheet(t *testing.T) {
	tabs := []tab{{SheetID: 0, Title: "Feuille 1", Index: 0}}
	_, repo := newFakeRepo(t, Config{}, nil, tabs...)

	boards, err := repo.ListBoards(context.Background())
	if err != nil {
		t.Fatalf("ListBoards failed: %v", err)
	}
	want := []domain.Board{{ID: "Feuille 1", Name: "Feuille 1"}}
	if !reflect.DeepEqual(boards, want) {
		t.Errorf("ListBoards returned %+v, want %+v", boards, want)
	}

	if _, err := repo.CreateBoard(context.Background(), domain.Board{ID: "other", Name: "Other"}); !errors.Is(err, domain.ErrBoardsUnsupported) {
		t.Errorf("Expected ErrBoardsUnsupported on CreateBoard, got %v", err)
	}
	if err := repo.DeleteBoard(context.Background(), "Feuille 1"); !errors.Is(err, domain.ErrBoardsUnsupported) {
		t.Errorf("Expected ErrBoardsUnsupported on DeleteBoard, got %v", err)
	}
}

// TestSheetsRepoDeleteCardBlanksTheRow covers the browser's delete button: the
// row is cleared in place and the next reads skip it.
func TestSheetsRepoDeleteCardBlanksTheRow(t *testing.T) {
	rows := [][]any{
		{"card_a", "Alpha", "To Do", "Low", "", "", ""},
		{"card_b", "Beta", "To Do", "Low", "", "", "card_a"},
	}
	fake, repo := newFakeRepo(t, Config{SheetName: "Sheet1"}, rows)

	if err := repo.DeleteCard(context.Background(), "tab", "card_a"); err != nil {
		t.Fatalf("DeleteCard failed: %v", err)
	}
	if len(fake.updates) != 1 || fake.updates[0].Range != "Sheet1!A2:G2" {
		t.Fatalf("Expected exactly one write blanking row 2, got %+v", fake.updates)
	}
	want := [][]string{{"", "", "", "", "", "", ""}}
	if got := flatten(fake.updates[0].Values); !reflect.DeepEqual(got, want) {
		t.Errorf("Expected the row blanked out, got %v", got)
	}

	// The deleted card is gone (the fake kept the written blank row), and the
	// dependent's BlockedBy cell still holds the text of the constraint: the
	// use case drops the reference before asking storage to delete.
	if _, err := repo.GetByID(context.Background(), "tab", "card_a"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected the row to read as deleted, got %v", err)
	}
	if got, _ := repo.GetByID(context.Background(), "tab", "card_b"); got.Title != "Beta" {
		t.Errorf("Expected the dependent untouched by storage, got %+v", got)
	}

	if err := repo.DeleteCard(context.Background(), "tab", "missing"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound, got %v", err)
	}
}

func TestParseSpreadsheetURL(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantID  string
		wantGID *int64
		wantErr bool
	}{
		{
			name:    "edit link with gid fragment",
			in:      "https://docs.google.com/spreadsheets/d/1Jll86VJaRlwOXFOocMihjag4bM5li1Mu-rmLObXeQCc/edit?usp=sharing#gid=0",
			wantID:  "1Jll86VJaRlwOXFOocMihjag4bM5li1Mu-rmLObXeQCc",
			wantGID: ptr(int64(0)),
		},
		{
			name:    "link without gid",
			in:      "https://docs.google.com/spreadsheets/d/abc-123/edit",
			wantID:  "abc-123",
			wantGID: nil,
		},
		{
			name:    "gid as query parameter",
			in:      "https://docs.google.com/spreadsheets/d/abc-123/edit?gid=42",
			wantID:  "abc-123",
			wantGID: ptr(int64(42)),
		},
		{
			name:   "bare id",
			in:     "1Jll86VJaRlwOXFOocMihjag4bM5li1Mu-rmLObXeQCc",
			wantID: "1Jll86VJaRlwOXFOocMihjag4bM5li1Mu-rmLObXeQCc",
		},
		{name: "no id", in: "https://docs.google.com/spreadsheets/", wantErr: true},
		{name: "empty", in: "   ", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, gid, err := ParseSpreadsheetURL(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Expected an error, got id=%q gid=%v", id, gid)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSpreadsheetURL failed: %v", err)
			}
			if id != tc.wantID {
				t.Errorf("Expected id %q, got %q", tc.wantID, id)
			}
			switch {
			case tc.wantGID == nil && gid != nil:
				t.Errorf("Expected no gid, got %d", *gid)
			case tc.wantGID != nil && gid == nil:
				t.Errorf("Expected gid %d, got none", *tc.wantGID)
			case tc.wantGID != nil && *gid != *tc.wantGID:
				t.Errorf("Expected gid %d, got %d", *tc.wantGID, *gid)
			}
		})
	}
}

func TestA1NameQuotesNamesWithSpaces(t *testing.T) {
	if got := a1Name("Sheet1"); got != "Sheet1" {
		t.Errorf("Plain name should stay unquoted, got %q", got)
	}
	if got := a1Name("My Sheet"); got != "'My Sheet'" {
		t.Errorf("Name with a space should be quoted, got %q", got)
	}
}

func ptr[T any](v T) *T { return &v }
