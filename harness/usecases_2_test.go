package harness

import (
	"context"
	"errors"
	"kanban-app/internal/domain"
	"testing"
)

func testUseCaseUpdateCardMovesACardBetweenColumnsAndGatesTheMove(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "A", "B", "C")
	early := addCard(t, uc, "Early")
	late := addCard(t, uc, "Late")
	setDependencies(t, uc, late, early.ID)

	// A partial edit keeps the fields it does not send.
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
