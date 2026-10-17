package gsheets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
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
		f.handleBatchUpdate(w, r)
		return
	}

	idx := strings.Index(r.URL.Path, "/values/")
	if idx < 0 {
		f.handleMetadata(w)
		return
	}

	rng := strings.TrimSuffix(r.URL.Path[idx+len("/values/"):], ":append")
	f.lastRange = rng

	name, _ := splitRange(rng)
	if !f.hasTab(name) {
		// What the real API answers for a range in a tab that does not exist.
		http.Error(w, `{"error":{"message":"Unable to parse range: `+rng+`"}}`, http.StatusBadRequest)
		return
	}
	isColumns := name == columnsSheet

	switch r.Method {
	case http.MethodGet:
		f.handleGet(w, rng, isColumns)
	case http.MethodPut, http.MethodPost:
		f.handleWrite(w, r, rng, isColumns, strings.Contains(r.URL.Path, ":append"))
	default:
		http.Error(w, "unsupported", http.StatusMethodNotAllowed)
	}
}

func (f *fakeSheets) handleMetadata(w http.ResponseWriter) {
	sheetsList := make([]map[string]any, 0, len(f.tabs))
	for _, t := range f.tabs {
		sheetsList = append(sheetsList, map[string]any{"properties": t})
	}
	json.NewEncoder(w).Encode(map[string]any{"sheets": sheetsList})
}

func (f *fakeSheets) handleBatchUpdate(w http.ResponseWriter, r *http.Request) {
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
}

func (f *fakeSheets) handleGet(w http.ResponseWriter, rng string, isColumns bool) {
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
}

func (f *fakeSheets) handleWrite(w http.ResponseWriter, r *http.Request, rng string, isColumns, isAppend bool) {
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
		f.applyAppend(call, isColumns)
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
}

func (f *fakeSheets) applyAppend(call valueCall, isColumns bool) {
	f.appends = append(f.appends, call)
	if isColumns {
		f.columnRows = append(f.columnRows, call.Values[0])
		return
	}
	f.rows = append(f.rows, call.Values[0])
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
