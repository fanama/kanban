package harness

import (
	"context"
	"errors"
	"kanban-app/internal/domain"
	"strings"
	"testing"
)

func TestUseCasesOverDynamicColumns(t *testing.T) {
	testUseCaseAddCardPutsTheCardInTheFirstColumn(t)
	testUseCaseAddCardCanTargetAnyExistingColumn(t)
	testUseCaseAddColumnAppendsAColumnAndValidatesItsName(t)
	testUseCaseMoveCardEnforcesTheStageRule(t)
	testUseCaseAConstraintPullsTheCardsThatDependOnIt(t)
	testUseCaseEditsPullDependents(t)
	testUseCaseAPullNeverBreaksAPromise(t)
	testUseCaseABackwardMoveStillDemotesTheDependents(t)
	testUseCaseACardWithNoConstraintMovesAnywhere(t)
	testUseCaseDemotingACardDragsItsDependentsDownTransitively(t)
	testUseCaseDeleteColumnMovesItsCardsToThePreviousColumn(t)
	testUseCaseRenameColumnReplacesAColumnSNameInPlace(t)
	testUseCaseDeletingTheFirstColumnHandsItsCardsToTheNewFirstOne(t)
	testUseCaseDeletingAColumnDemotesTheCardsThatDependOnTheMovedOnes(t)
	testUseCaseTheBoardKeepsAtLeastOneColumn(t)
	testUseCaseUpdateCardMovesACardBetweenColumnsAndGatesTheMove(t)
	testUseCaseConstraintsAreValidated(t)
	testUseCaseReordersCardsInsideAColumn(t)
}

func testUseCaseAddCardPutsTheCardInTheFirstColumn(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t)
	card := addCard(t, uc, "First")
	if card.ColumnID != "To Do" {
		t.Errorf("Expected the card in To Do, got %q", card.ColumnID)
	}
	if _, err := uc.AddCard(ctx, "   ", domain.PriorityLow); !errors.Is(err, domain.ErrEmptyTitle) {
		t.Errorf("Expected ErrEmptyTitle, got %v", err)
	}
}

func testUseCaseAddCardCanTargetAnyExistingColumn(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t)
	if _, err := uc.AddCardTo(ctx, "Doing", "Placed in Doing", domain.PriorityHigh); err != nil {
		t.Fatalf("AddCardTo failed: %v", err)
	}
	if got := columnOf(t, uc, "Placed in Doing"); got != "Doing" {
		t.Errorf("Expected the card in Doing, got %q", got)
	}
	if _, err := uc.AddCardTo(ctx, "ghost", "No column", domain.PriorityLow); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected ErrColumnNotFound, got %v", err)
	}
}

func testUseCaseAddColumnAppendsAColumnAndValidatesItsName(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "To Do", "Doing")

	board, err := uc.AddColumn(ctx, "  Review  ")
	if err != nil {
		t.Fatalf("AddColumn failed: %v", err)
	}
	if len(board.Columns) != 3 {
		t.Fatalf("Expected 3 columns, got %d", len(board.Columns))
	}
	last := board.Columns[2].Column
	if last.Name != "Review" || last.Position != 2 {
		t.Errorf("Expected a trimmed column appended at the end, got %+v", last)
	}

	if _, err := uc.AddColumn(ctx, "  "); !errors.Is(err, domain.ErrEmptyColumnName) {
		t.Errorf("Expected ErrEmptyColumnName, got %v", err)
	}
	if _, err := uc.AddColumn(ctx, "review"); !errors.Is(err, domain.ErrColumnExists) {
		t.Errorf("Expected ErrColumnExists, got %v", err)
	}
}

func testUseCaseMoveCardEnforcesTheStageRule(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "To Do", "Doing", "Done")
	early := addCard(t, uc, "Early")
	late := addCard(t, uc, "Late")

	// Move Late to Doing first: Early is still at To Do, which is the stage
	// Doing requires (n-1 = 0).
	if _, err := uc.MoveCard(ctx, late.ID, "Doing"); err != nil {
		t.Fatalf("Moving to the second column must be allowed: %v", err)
	}
	setDependencies(t, uc, late, early.ID)

	// Reaching Done requires Early at Doing or further, it is still at To Do.
	_, err := uc.MoveCard(ctx, late.ID, "Done")
	if !errors.Is(err, domain.ErrCardBlocked) {
		t.Fatalf("Expected ErrCardBlocked, got %v", err)
	}
	if !strings.Contains(err.Error(), "Early") || !strings.Contains(err.Error(), "Doing") {
		t.Errorf("Expected the blocking card and the required stage in %q", err)
	}

	// With the constraint one stage further, the card may advance.
	if _, err := uc.MoveCard(ctx, early.ID, "Doing"); err != nil {
		t.Fatalf("Moving the constraint failed: %v", err)
	}
	if _, err := uc.MoveCard(ctx, late.ID, "Done"); err != nil {
		t.Errorf("Expected the card to advance once its constraint moved: %v", err)
	}
}

func testUseCaseAConstraintPullsTheCardsThatDependOnIt(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "To Do", "Doing", "Done")
	constraint := addCard(t, uc, "The constraint")
	dependent := addCard(t, uc, "The dependent")
	setDependencies(t, uc, dependent, constraint.ID)

	// Both sit in To Do. The constraint moves to Doing and the dependent is
	// carried along — a constraint never passes its dependents, so they go
	// together.
	if _, err := uc.MoveCard(ctx, constraint.ID, "Doing"); err != nil {
		t.Fatalf("Moving the constraint failed: %v", err)
	}
	if got := columnOf(t, uc, "The constraint"); got != "Doing" {
		t.Errorf("Expected the constraint in Doing, got %q", got)
	}
	if got := columnOf(t, uc, "The dependent"); got != "Doing" {
		t.Errorf("Expected the dependent pulled to Doing, got %q", got)
	}

	// The constraint leaps to Done: the dependent follows one stage after.
	if _, err := uc.MoveCard(ctx, constraint.ID, "Done"); err != nil {
		t.Fatalf("Moving the constraint on failed: %v", err)
	}
	if got := columnOf(t, uc, "The dependent"); got != "Done" {
		t.Errorf("Expected the dependent pulled to Done too, got %q", got)
	}
}

func testUseCaseEditsPullDependents(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "To Do", "Doing", "Done")
	constraint := addCard(t, uc, "Constraint")
	dependent := addCard(t, uc, "Dependent")
	setDependencies(t, uc, dependent, constraint.ID)

	// An edit that jumps the constraint to Done carries the dependent with
	// it.
	jump := constraint
	jump.ColumnID = "Done"
	jump.DependsOn = nil // partial edit: leave the constraints alone
	if _, err := uc.UpdateCard(ctx, jump); err != nil {
		t.Fatalf("UpdateCard failed: %v", err)
	}
	if got := columnOf(t, uc, "Dependent"); got != "Done" {
		t.Errorf("Expected the dependent pulled to Done, got %q", got)
	}
}

// testUseCaseAPullNeverBreaksAPromise checks that a pulled card whose own
// constraint has not reached the required stage stays where it is.
func testUseCaseAPullNeverBreaksAPromise(t *testing.T) {
	ctx := context.Background()
	uc2 := newBoard(t, "A", "B", "C")
	ground := addCard(t, uc2, "Ground")
	middle := addCard(t, uc2, "Middle")
	top := addCard(t, uc2, "Top")
	setDependencies(t, uc2, middle, ground.ID)
	setDependencies(t, uc2, top, middle.ID)

	// Middle moves to B; the stage rule allows it (Ground is at A, and a
	// target of B requires a constraint at A at least). Top depends on
	// Middle and is pulled to B as well; Ground is Middle's constraint, it
	// has no dependent to pull.
	if _, err := uc2.MoveCard(ctx, middle.ID, "B"); err != nil {
		t.Fatalf("Moving Middle failed: %v", err)
	}
	if got := columnOf(t, uc2, "Ground"); got != "A" {
		t.Errorf("Expected Ground untouched (nothing depends on it), got %q", got)
	}
	if got := columnOf(t, uc2, "Top"); got != "B" {
		t.Errorf("Expected Top pulled to B with Middle, got %q", got)
	}

	// Top then leaps to C: Middle is pulled to B's successor... but the
	// stage rule for Middle at C requires Ground at B, and Ground sits in
	// A: Middle cannot be pulled that far and stays put (no deadlock, and
	// no broken promise).
	if _, err := uc2.MoveCard(ctx, top.ID, "C"); err != nil {
		t.Fatalf("Moving Top failed: %v", err)
	}
	if got := columnOf(t, uc2, "Middle"); got != "B" {
		t.Errorf("Expected Middle to stay in B (Ground blocks a pull to C), got %q", got)
	}
	if got := columnOf(t, uc2, "Ground"); got != "A" {
		t.Errorf("Expected Ground untouched, got %q", got)
	}
}

func testUseCaseABackwardMoveStillDemotesTheDependents(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "To Do", "Doing", "Done")
	constraint := addCard(t, uc, "Constraint")
	dependent := addCard(t, uc, "Dependent")
	setDependencies(t, uc, dependent, constraint.ID)

	if _, err := uc.MoveCard(ctx, constraint.ID, "Done"); err != nil {
		t.Fatalf("Moving the constraint failed: %v", err)
	}
	if got := columnOf(t, uc, "Dependent"); got != "Done" {
		t.Fatalf("Expected the dependent pulled along, got %q", got)
	}

	// Going back demotes both, as before.
	if _, err := uc.MoveCard(ctx, constraint.ID, "To Do"); err != nil {
		t.Fatalf("A backward move must always be allowed: %v", err)
	}
	if got := columnOf(t, uc, "Dependent"); got != "Doing" {
		t.Errorf("Expected the dependent demoted to Doing, got %q", got)
	}
}

func testUseCaseACardWithNoConstraintMovesAnywhere(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "To Do", "Doing", "Done")
	card := addCard(t, uc, "Free")

	if _, err := uc.MoveCard(ctx, card.ID, "Done"); err != nil {
		t.Errorf("Expected a free card to reach the last column: %v", err)
	}
	if _, err := uc.MoveCard(ctx, card.ID, "To Do"); err != nil {
		t.Errorf("Expected a free card to go back: %v", err)
	}
}

func testUseCaseDemotingACardDragsItsDependentsDownTransitively(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "A", "B", "C", "D")
	base := addCard(t, uc, "Base")
	dependent := addCard(t, uc, "Dependent")
	grandchild := addCard(t, uc, "Grandchild")

	// Free of constraints, the three cards move to D; the dependencies are
	// then declared, as one would build the board in practice.
	for _, card := range []domain.Card{base, dependent, grandchild} {
		if _, err := uc.MoveCard(ctx, card.ID, "D"); err != nil {
			t.Fatalf("Moving %q to D failed: %v", card.Title, err)
		}
	}
	setDependencies(t, uc, dependent, base.ID)
	setDependencies(t, uc, grandchild, dependent.ID)

	// Base goes back to the first column: Dependents follow.
	if _, err := uc.MoveCard(ctx, base.ID, "A"); err != nil {
		t.Fatalf("Moving Base back failed: %v", err)
	}

	if got := columnOf(t, uc, "Base"); got != "A" {
		t.Errorf("Expected Base in A, got %q", got)
	}
	if got := columnOf(t, uc, "Dependent"); got != "B" {
		t.Errorf("Expected Dependent just after Base (B), got %q", got)
	}
	if got := columnOf(t, uc, "Grandchild"); got != "C" {
		t.Errorf("Expected Grandchild to follow (C), got %q", got)
	}
}

func testUseCaseDeleteColumnMovesItsCardsToThePreviousColumn(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "To Do", "Doing", "Done")
	card := addCard(t, uc, "In the middle")
	if _, err := uc.MoveCard(ctx, card.ID, "Doing"); err != nil {
		t.Fatalf("MoveCard failed: %v", err)
	}

	board, err := uc.DeleteColumn(ctx, "Doing")
	if err != nil {
		t.Fatalf("DeleteColumn failed: %v", err)
	}
	if len(board.Columns) != 2 || board.Columns[0].Column.Name != "To Do" || board.Columns[1].Column.Name != "Done" {
		t.Fatalf("Expected the column to be gone, got %+v", board.OrderedColumns())
	}
	if got := columnOf(t, uc, "In the middle"); got != "To Do" {
		t.Errorf("Expected the card to fall back to To Do, got %q", got)
	}
	if board.Columns[1].Column.Position != 1 {
		t.Errorf("Expected the surviving columns to be renumbered, got %+v", board.Columns[1].Column)
	}
}

func testUseCaseRenameColumnReplacesAColumnSNameInPlace(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "To Do", "Doing", "Done")
	card := addCard(t, uc, "Busy")
	if _, err := uc.MoveCard(ctx, card.ID, "Doing"); err != nil {
		t.Fatalf("MoveCard failed: %v", err)
	}

	board, err := uc.RenameColumn(ctx, "Doing", "Work in progress")
	if err != nil {
		t.Fatalf("RenameColumn failed: %v", err)
	}
	if len(board.Columns) != 3 || board.Columns[1].Column.Name != "Work in progress" {
		t.Fatalf("Expected the second column to be renamed in place, got %+v", board.OrderedColumns())
	}
	if board.Columns[1].Column.Position != 1 || board.Columns[1].Column.ID != "Doing" {
		t.Errorf("Expected position and ID untouched, got %+v", board.Columns[1].Column)
	}
	if got := columnOf(t, uc, "Busy"); got != "Work in progress" {
		t.Errorf("Expected the card to follow the rename, got %q", got)
	}

	// The usual guards: a blank new name, a sibling's name (case-insensitive)
	// and an unknown column are refused. Columns are addressed by ID, so the
	// renamed column is still "Doing".
	if _, err := uc.RenameColumn(ctx, "Doing", "  "); !errors.Is(err, domain.ErrEmptyColumnName) {
		t.Errorf("Expected ErrEmptyColumnName on a blank new name, got %v", err)
	}
	if _, err := uc.RenameColumn(ctx, "Doing", "done"); !errors.Is(err, domain.ErrColumnExists) {
		t.Errorf("Expected ErrColumnExists on a case-insensitive duplicate, got %v", err)
	}
	if _, err := uc.RenameColumn(ctx, "nope", "X"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected ErrColumnNotFound, got %v", err)
	}
	// Clearing the rename failure left the board as it was.
	if got := columnOf(t, uc, "Busy"); got != "Work in progress" {
		t.Errorf("Expected the failed renames to change nothing, got %q", got)
	}
}

func testUseCaseDeletingTheFirstColumnHandsItsCardsToTheNewFirstOne(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "To Do", "Doing", "Done")
	addCard(t, uc, "Started")

	if _, err := uc.DeleteColumn(ctx, "To Do"); err != nil {
		t.Fatalf("DeleteColumn failed: %v", err)
	}
	if got := columnOf(t, uc, "Started"); got != "Doing" {
		t.Errorf("Expected the card in the new first column, got %q", got)
	}
}

func testUseCaseDeletingAColumnDemotesTheCardsThatDependOnTheMovedOnes(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "A", "B", "C")
	base := addCard(t, uc, "Base")
	dependent := addCard(t, uc, "Dependent")

	// The dependent moves first: its constraint must not pass it.
	if _, err := uc.MoveCard(ctx, dependent.ID, "B"); err != nil {
		t.Fatalf("MoveCard failed: %v", err)
	}
	setDependencies(t, uc, dependent, base.ID)
	if _, err := uc.MoveCard(ctx, base.ID, "B"); err != nil {
		t.Fatalf("MoveCard failed: %v", err)
	}
	if _, err := uc.MoveCard(ctx, dependent.ID, "C"); err != nil {
		t.Fatalf("MoveCard failed: %v", err)
	}

	// Deleting B sends Base back to A; Dependent cannot stay in C.
	if _, err := uc.DeleteColumn(ctx, "B"); err != nil {
		t.Fatalf("DeleteColumn failed: %v", err)
	}
	if got := columnOf(t, uc, "Base"); got != "A" {
		t.Errorf("Expected Base in A, got %q", got)
	}
	if got := columnOf(t, uc, "Dependent"); got != "C" {
		t.Errorf("Expected Dependent to stay in C (one stage behind Base), got %q", got)
	}
}

func testUseCaseTheBoardKeepsAtLeastOneColumn(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "Only")
	if _, err := uc.DeleteColumn(ctx, "Only"); !errors.Is(err, domain.ErrLastColumn) {
		t.Errorf("Expected ErrLastColumn, got %v", err)
	}
	if _, err := uc.DeleteColumn(ctx, "nope"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected ErrColumnNotFound, got %v", err)
	}
	if _, err := uc.MoveCard(ctx, "nope", "Only"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound, got %v", err)
	}
}
