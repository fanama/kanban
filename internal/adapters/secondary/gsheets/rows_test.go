package gsheets

import (
	"context"
	"errors"
	"kanban-app/internal/domain"
	"reflect"
	"testing"
)

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
	testSheetsGetByID(t, repo)
}

func testSheetsGetByID(t *testing.T, repo *Repo) {
	t.Helper()
	ctx := context.Background()

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
