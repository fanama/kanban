package gsheets

import (
	"context"
	"errors"
	"kanban-app/internal/domain"
	"net/http/httptest"
	"reflect"
	"testing"
)

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
		case "Sheet1!A1:H1":
			// The order and the names are a contract with the spreadsheets
			// written by earlier versions: renaming a cell orphans its data.
			want := []string{"ID", "Title", "Status", "Priority", "Description", "Objective", "BlockedBy", "Version"}
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
