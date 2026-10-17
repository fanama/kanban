package harness

import (
	"errors"
	"reflect"
	"testing"

	"kanban-app/internal/domain"
)

func contractDeletesCard(t *testing.T, s *contractState) {
	t.Helper()

	// contract_1 depends on contract_0: deleting the constraint drops it from
	// the dependent's list instead of leaving a ghost.
	if err := s.repo.DeleteCard(s.ctx, s.boardA.ID, "contract_0"); err != nil {
		t.Fatalf("DeleteCard failed: %v", err)
	}
	if _, err := s.repo.GetByID(s.ctx, s.boardA.ID, "contract_0"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected the deleted card to be gone, got %v", err)
	}
	got, err := s.repo.GetByID(s.ctx, s.boardA.ID, s.card.ID)
	if err != nil {
		t.Fatalf("GetByID after DeleteCard failed: %v", err)
	}
	if len(got.DependsOn) != 0 {
		t.Errorf("Expected the constraint to be dropped from the dependent, got %v", got.DependsOn)
	}
	if err := s.repo.DeleteCard(s.ctx, s.boardA.ID, "missing"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound when deleting twice, got %v", err)
	}
}

func contractDeletesColumn(t *testing.T, s *contractState) {
	t.Helper()

	// Deleting renumbers the survivors, so the next column created lands after
	// them and the ordering stays dense.
	if err := s.repo.DeleteColumn(s.ctx, s.boardA.ID, "col_b"); err != nil {
		t.Fatalf("DeleteColumn failed: %v", err)
	}
	columns, err := s.repo.GetColumns(s.ctx, s.boardA.ID)
	if err != nil {
		t.Fatalf("GetColumns after DeleteColumn failed: %v", err)
	}
	if !reflect.DeepEqual(columns, []domain.Column{s.wanted[0], {ID: "col_c", Name: "Done", Position: 1}}) {
		t.Errorf("Expected the survivors renumbered, got %+v", columns)
	}

	// The cards of a deleted column are the use case's business: storage must
	// not touch them.
	got, err := s.repo.GetByID(s.ctx, s.boardA.ID, s.card.ID)
	if err != nil {
		t.Fatalf("GetByID after DeleteColumn failed: %v", err)
	}
	if got.ColumnID != "col_b" || got.Title != "Contract, edited" {
		t.Errorf("DeleteColumn must not touch the cards, got %+v", got)
	}
	if err := s.repo.DeleteColumn(s.ctx, s.boardA.ID, "missing"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected ErrColumnNotFound, got %v", err)
	}
}

func contractReplacesBoard(t *testing.T, s *contractState) {
	t.Helper()

	importedColumns := []domain.Column{
		{ID: "col_backlog", Name: "Backlog", Position: 0},
		{ID: "col_wip", Name: "WIP", Position: 1},
	}
	importedCards := []domain.Card{
		{ID: "imp_0", Title: "Seed", ColumnID: "col_backlog", Priority: domain.PriorityLow},
		{ID: "imp_1", Title: "Imported", Description: "From a file", ColumnID: "col_wip",
			DependsOn: []string{"imp_0"}, Priority: domain.PriorityHigh},
	}
	if err := s.repo.ReplaceBoard(s.ctx, s.boardA.ID, importedColumns, importedCards); err != nil {
		t.Fatalf("ReplaceBoard failed: %v", err)
	}

	columns, err := s.repo.GetColumns(s.ctx, s.boardA.ID)
	if err != nil {
		t.Fatalf("GetColumns after ReplaceBoard failed: %v", err)
	}
	if !reflect.DeepEqual(columns, importedColumns) {
		t.Errorf("Expected the imported columns, got %+v", columns)
	}
	for _, want := range importedCards {
		got, err := s.repo.GetByID(s.ctx, s.boardA.ID, want.ID)
		if err != nil {
			t.Fatalf("GetByID(%s) failed: %v", want.ID, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("GetByID(%s) returned %+v, want %+v", want.ID, got, want)
		}
	}

}

func contractReplacedBoardIsClean(t *testing.T, s *contractState) {
	t.Helper()

	// Everything the board used to hold is gone.
	if _, err := s.repo.GetByID(s.ctx, s.boardA.ID, s.card.ID); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected the old cards to be gone, got %v", err)
	}
	if err := s.repo.DeleteColumn(s.ctx, s.boardA.ID, "col_a"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected the old columns to be gone, got %v", err)
	}
}

func contractDeletesBoards(t *testing.T, s *contractState) {
	t.Helper()

	// Board B still holds whatever it had — here, nothing: the import of A
	// must not have reached it.
	bCards, err := s.repo.GetAll(s.ctx, s.boardB.ID)
	if err != nil {
		t.Fatalf("GetAll on board B failed: %v", err)
	}
	if len(bCards) != 0 {
		t.Errorf("Expected the import to stay inside board A, got %v", bCards)
	}

	if err := s.repo.DeleteBoard(s.ctx, s.boardB.ID); err != nil {
		t.Fatalf("DeleteBoard failed: %v", err)
	}
	if _, err := s.repo.GetColumns(s.ctx, s.boardB.ID); !errors.Is(err, domain.ErrBoardNotFound) {
		t.Errorf("Expected the deleted board to be gone, got %v", err)
	}
	boards, err := s.repo.ListBoards(s.ctx)
	if err != nil {
		t.Fatalf("ListBoards failed: %v", err)
	}
	if len(boards) != 1 || boards[0].ID != s.boardA.ID {
		t.Errorf("Expected only board A to remain, got %+v", boards)
	}
}

func contractDeletesEveryBoard(t *testing.T, s *contractState) {
	t.Helper()

	// Board A keeps working after its sibling is gone.
	if err := s.repo.DeleteBoard(s.ctx, s.boardA.ID); err != nil {
		t.Fatalf("DeleteBoard failed: %v", err)
	}
	if _, err := s.repo.GetAll(s.ctx, s.boardA.ID); !errors.Is(err, domain.ErrBoardNotFound) {
		t.Errorf("Expected deleting a board to take its cards, got %v", err)
	}
	boards, err := s.repo.ListBoards(s.ctx)
	if err != nil {
		t.Fatalf("ListBoards failed: %v", err)
	}
	if len(boards) != 0 {
		t.Errorf("Expected an empty list of boards, got %+v", boards)
	}
	if err := s.repo.DeleteBoard(s.ctx, "missing"); !errors.Is(err, domain.ErrBoardNotFound) {
		t.Errorf("Expected ErrBoardNotFound, got %v", err)
	}
}

func containsCard(cards []domain.Card, want domain.Card) bool {
	for _, c := range cards {
		if reflect.DeepEqual(c, want) {
			return true
		}
	}
	return false
}
