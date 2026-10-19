package harness

import (
	"context"
	"errors"
	"kanban-app/internal/domain"
	"reflect"
	"testing"
)

func testUseCaseUpdateCardMovesACardBetweenColumnsAndGatesTheMove(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "A", "B", "C")
	early := addCard(t, uc, "Early")
	late := addCard(t, uc, "Late")
	setDependencies(t, uc, late, early.ID)

	// A partial edit keeps the fields it does not send.
	late = findCard(t, uc, "Late")
	edited := late
	edited.Title = "Late, renamed"
	edited.DependsOn = nil
	edited.ColumnID = "C"
	if _, err := uc.UpdateCard(ctx, edited); !errors.Is(err, domain.ErrCardBlocked) {
		t.Fatalf("Expected ErrCardBlocked for a jump to the last column, got %v", err)
	}

	edited.ColumnID = "B"
	board, err := uc.UpdateCard(ctx, edited)
	if err != nil {
		t.Fatalf("UpdateCard failed: %v", err)
	}
	for _, c := range board.All() {
		if c.ID == late.ID {
			if len(c.DependsOn) != 1 || c.DependsOn[0] != early.ID {
				t.Errorf("A nil constraint list must keep the stored constraints, got %v", c.DependsOn)
			}
			if c.ColumnID != "B" || c.Title != "Late, renamed" {
				t.Errorf("Unexpected card after the edit: %+v", c)
			}
		}
	}

	if _, err := uc.UpdateCard(ctx, domain.Card{ID: late.ID, Title: "Ghost", ColumnID: "nope"}); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected ErrColumnNotFound, got %v", err)
	}
}

func testUseCaseReordersCardsInsideAColumn(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "To Do", "Doing")
	first := addCard(t, uc, "First")
	addCard(t, uc, "Second")
	third := addCard(t, uc, "Third")

	board, err := uc.ReorderCard(ctx, third.ID, first.ID)
	if err != nil {
		t.Fatalf("ReorderCard failed: %v", err)
	}
	column, _ := board.Column("To Do")
	want := []string{third.ID, first.ID, findCard(t, uc, "Second").ID}
	if got := cardIDs(column.Cards); !reflect.DeepEqual(got, want) {
		t.Errorf("Expected %v, got %v", want, got)
	}

	board, err = uc.ReorderCard(ctx, third.ID, "")
	if err != nil {
		t.Fatalf("ReorderCard to the end failed: %v", err)
	}
	column, _ = board.Column("To Do")
	want = []string{first.ID, findCard(t, uc, "Second").ID, third.ID}
	if got := cardIDs(column.Cards); !reflect.DeepEqual(got, want) {
		t.Errorf("Expected the appended column %v, got %v", want, got)
	}

	if _, err := uc.AddCardTo(ctx, "Doing", "Elsewhere", domain.PriorityLow); err != nil {
		t.Fatalf("AddCardTo failed: %v", err)
	}
	elsewhere := findCard(t, uc, "Elsewhere")
	if _, err := uc.ReorderCard(ctx, third.ID, elsewhere.ID); !errors.Is(err, domain.ErrNotSameColumn) {
		t.Errorf("Expected ErrNotSameColumn, got %v", err)
	}
	if _, err := uc.ReorderCard(ctx, third.ID, "missing"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound, got %v", err)
	}
}

func cardIDs(cards []domain.Card) []string {
	ids := make([]string, 0, len(cards))
	for _, card := range cards {
		ids = append(ids, card.ID)
	}
	return ids
}

func testUseCaseConstraintsAreValidated(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "A", "B")
	card := addCard(t, uc, "Card")

	ghost := card
	ghost.DependsOn = []string{"does-not-exist"}
	if _, err := uc.UpdateCard(ctx, ghost); !errors.Is(err, domain.ErrUnknownDependency) {
		t.Errorf("Expected ErrUnknownDependency, got %v", err)
	}

	self := card
	self.DependsOn = []string{card.ID}
	if _, err := uc.UpdateCard(ctx, self); !errors.Is(err, domain.ErrSelfDependency) {
		t.Errorf("Expected ErrSelfDependency, got %v", err)
	}

	other := addCard(t, uc, "Other")
	setDependencies(t, uc, other, card.ID)
	cyclic := card
	cyclic.DependsOn = []string{other.ID}
	if _, err := uc.UpdateCard(ctx, cyclic); !errors.Is(err, domain.ErrDependencyCycle) {
		t.Errorf("Expected ErrDependencyCycle, got %v", err)
	}
}
