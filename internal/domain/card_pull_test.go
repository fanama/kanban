package domain

import (
	"errors"
	"strings"
	"testing"
)

// newBoard builds a read model with the given columns, placing each card in the
// column whose ID matches card.ColumnID.
func newBoardView(columns []string, cards ...Card) BoardView {
	view := BoardView{Columns: make([]ColumnView, len(columns))}
	for i, id := range columns {
		view.Columns[i] = ColumnView{Column: Column{ID: id, Name: id, Position: i}}
	}
	for _, card := range cards {
		for i := range view.Columns {
			if view.Columns[i].Column.ID == card.ColumnID {
				view.Columns[i].Cards = append(view.Columns[i].Cards, card)
				break
			}
		}
	}
	return view
}

func TestPullPlanPullsDirectDependents(t *testing.T) {
	board := newBoardView([]string{"A", "B"},
		Card{ID: "a", Title: "Alpha", ColumnID: "A"},
		Card{ID: "b", Title: "Beta", ColumnID: "A", DependsOn: []string{"a"}},
	)

	steps, err := PullPlan(Card{ID: "a"}, 1, board)
	if err != nil {
		t.Fatalf("PullPlan failed: %v", err)
	}
	if len(steps) != 1 || steps[0].CardID != "b" || steps[0].TargetColumn.ID != "B" {
		t.Fatalf("Expected Beta pulled to B, got %+v (err %v)", steps, err)
	}
}

func TestPullPlanPullsTransitively(t *testing.T) {
	board := newBoardView([]string{"A", "B", "C"},
		Card{ID: "a", Title: "Alpha", ColumnID: "A"},
		Card{ID: "b", Title: "Beta", ColumnID: "A", DependsOn: []string{"a"}},
		Card{ID: "c", Title: "Gamma", ColumnID: "A", DependsOn: []string{"b"}},
	)

	steps, err := PullPlan(Card{ID: "a"}, 2, board)
	if err != nil {
		t.Fatalf("PullPlan failed: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("Expected two steps, got %+v", steps)
	}
	for _, step := range steps {
		if step.TargetColumn.Position != 2 {
			t.Errorf("Expected every member of the chain carried to C, got %+v", step)
		}
	}
}

func TestPullPlanSkipsAlreadyCloseEnough(t *testing.T) {
	board := newBoardView([]string{"A", "B", "C"},
		Card{ID: "a", Title: "Alpha", ColumnID: "A"},
		Card{ID: "b", Title: "Beta", ColumnID: "B", DependsOn: []string{"a"}},
	)

	// Alpha moves from A to C; Beta already sits in B, in front of Alpha's new
	// stage… actually behind it, but the push happens with a pull: Beta is
	// carried to C with Alpha, so the chain stays moving.
	steps, err := PullPlan(Card{ID: "a"}, 2, board)
	if err != nil {
		t.Fatalf("PullPlan failed: %v", err)
	}
	if len(steps) != 1 || steps[0].CardID != "b" {
		t.Errorf("Expected Beta pulled along, got %+v", steps)
	}
}

func TestPullPlanRefusedWhenAPullWouldBreakAPromise(t *testing.T) {
	// Alpha is the far-back constraint of the chain, Beta the middle, Gamma the
	// front. Pulling the chain forward must respect each card's own promise.
	board := newBoardView([]string{"A", "B", "C"},
		Card{ID: "alpha", Title: "Alpha", ColumnID: "A"},
		Card{ID: "beta", Title: "Beta", ColumnID: "A", DependsOn: []string{"alpha"}},
		Card{ID: "gamma", Title: "Gamma", ColumnID: "A", DependsOn: []string{"beta"}},
	)

	// Beta jumps straight to C: Gamma is pulled to C too, but Gamma's promise
	// needs at least Beta at B after the plan… which the plan provides (Beta
	// itself moves to C), and Alpha stays behind. Gamma at C sits with its own
	// constraint Beta at C: Beta >= C-1 = B. That is satisfied.
	steps, err := PullPlan(Card{ID: "beta"}, 2, board)
	if err != nil {
		t.Fatalf("PullPlan to C should succeed: %v", err)
	}
	if len(steps) != 1 || steps[0].CardID != "gamma" {
		t.Fatalf("Expected Gamma pulled to C, got %+v", steps)
	}

	// However an intermediate promise can fail: put Alpha INSIDE the promise
	// chain — Gamma depends on Alpha, and Gamma is pulled by Beta.
	board = newBoardView([]string{"A", "B", "C", "D"},
		Card{ID: "alpha", Title: "Alpha", ColumnID: "A"},
		Card{ID: "beta", Title: "Beta", ColumnID: "A", DependsOn: []string{"alpha"}},
		Card{ID: "gamma", Title: "Gamma", ColumnID: "B", DependsOn: []string{"beta", "alpha"}},
	)

	// Beta leaps to D. Gamma is pulled to D, but Gamma's external constraint
	// Alpha sits in A; the stage rule needs Alpha at C at least: the pull must
	// be refused rather than silently break Gamma's promise.
	_, err = PullPlan(Card{ID: "beta"}, 3, board)
	if !errors.Is(err, ErrWouldLeaveDependents) {
		t.Fatalf("Expected ErrWouldLeaveDependents, got %v", err)
	}
	if !strings.Contains(err.Error(), "Gamma") {
		t.Errorf("Expected the refused pull to name the card, got %q", err)
	}
}

func TestPullPlanLeavesConstraintsBehind(t *testing.T) {
	// Pulling never goes up the *constraint* direction: Beta depends on Alpha,
	// moving Beta forward must not move Alpha.
	board := newBoardView([]string{"A", "B"},
		Card{ID: "a", Title: "Alpha", ColumnID: "A"},
		Card{ID: "b", Title: "Beta", ColumnID: "A", DependsOn: []string{"a"}},
	)

	steps, err := PullPlan(Card{ID: "b"}, 1, board)
	if err != nil {
		t.Fatalf("PullPlan failed: %v", err)
	}
	if len(steps) != 0 {
		t.Errorf("Expected no steps: a dependent has no dependents of its own, got %+v", steps)
	}
}
