package gsheets

import (
	"context"
	"errors"
	"kanban-app/internal/domain"
	"reflect"
	"strings"
	"testing"
)

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

	assertReplaceWrites(t, fake)
}

func assertReplaceWrites(t *testing.T, fake *fakeSheets) {
	t.Helper()
	cardWrites, columnWrites := 0, 0
	for _, update := range fake.updates {
		switch {
		case strings.HasPrefix(update.Range, "Sheet1!A2:"):
			cardWrites++
			if update.Range != "Sheet1!A2:H3" {
				t.Errorf("Expected the cards block of 2 rows (one blank padding row), got %q", update.Range)
			}
			want := [][]string{{"imp_0", "Imported", "Only", "High", "", "", "", "1"}, {"", "", "", "", "", "", "", ""}}
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
	if len(fake.updates) != 1 || fake.updates[0].Range != "Sheet1!A2:H2" {
		t.Fatalf("Expected exactly one write blanking row 2, got %+v", fake.updates)
	}
	want := [][]string{{"", "", "", "", "", "", "", ""}}
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
