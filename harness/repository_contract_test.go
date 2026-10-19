package harness

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	memoryRepo "kanban-app/internal/adapters/secondary/memory"
	sqliteRepo "kanban-app/internal/adapters/secondary/sqlite"
	"kanban-app/internal/domain"
	"kanban-app/internal/ports"
)

// TestRepositoryContract drives the output port the same way against every
// adapter: swapping storage must not change the behaviour the core relies on,
// columns and card constraints included.
func TestRepositoryContract(t *testing.T) {
	factories := map[string]func(t *testing.T) ports.BoardRepository{
		"memory": func(t *testing.T) ports.BoardRepository {
			return memoryRepo.NewMemoryRepo()
		},
		"sqlite": func(t *testing.T) ports.BoardRepository {
			repo, err := sqliteRepo.Open(context.Background(), filepath.Join(t.TempDir(), "cards.db"))
			if err != nil {
				t.Fatalf("Opening the SQLite repository failed: %v", err)
			}
			t.Cleanup(func() { repo.Close() })
			return repo
		},
	}

	for name, factory := range factories {
		t.Run(name, func(t *testing.T) { contract(t, factory(t)) })
	}
}

// contractState carries the boards, columns and card the phases build on.
type contractState struct {
	repo   ports.BoardRepository
	ctx    context.Context
	boardA domain.Board
	boardB domain.Board
	wanted []domain.Column
	card   domain.Card
}

func contract(t *testing.T, repo ports.BoardRepository) {
	t.Helper()
	state := &contractState{repo: repo, ctx: context.Background()}
	contractBoards(t, state)
	contractColumns(t, state)
	contractRenamesColumns(t, state)
	contractCards(t, state)
	contractReordersCards(t, state)
	contractCardReads(t, state)
	contractCardUpdates(t, state)
	contractCardMoves(t, state)
	contractDeletesCard(t, state)
	contractDeletesColumn(t, state)
	contractReplacesBoard(t, state)
	contractReplacedBoardIsClean(t, state)
	contractDeletesBoards(t, state)
	contractDeletesEveryBoard(t, state)
}

func contractBoards(t *testing.T, s *contractState) {
	t.Helper()

	boards, err := s.repo.ListBoards(s.ctx)
	if err != nil {
		t.Fatalf("ListBoards failed: %v", err)
	}
	if len(boards) != 0 {
		t.Fatalf("Expected no board yet, got %v", boards)
	}

	s.boardA = domain.Board{ID: "bda", Name: "Board A"}
	if _, err := s.repo.CreateBoard(s.ctx, s.boardA); err != nil {
		t.Fatalf("CreateBoard failed: %v", err)
	}
	s.boardB = domain.Board{ID: "bdb", Name: "Board B"}
	if _, err := s.repo.CreateBoard(s.ctx, s.boardB); err != nil {
		t.Fatalf("CreateBoard failed: %v", err)
	}
	if _, err := s.repo.CreateBoard(s.ctx, domain.Board{ID: "bda", Name: "Other"}); !errors.Is(err, domain.ErrBoardExists) {
		t.Errorf("Expected ErrBoardExists on a duplicate board, got %v", err)
	}
}

func contractColumns(t *testing.T, s *contractState) {
	t.Helper()

	columns, err := s.repo.GetColumns(s.ctx, s.boardA.ID)
	if err != nil {
		t.Fatalf("GetColumns failed: %v", err)
	}
	if len(columns) != 0 {
		t.Fatalf("Expected an empty board, got %v", columns)
	}

	s.wanted = []domain.Column{
		{ID: "col_a", Name: "To Do", Position: 0},
		{ID: "col_b", Name: "Doing", Position: 1},
		{ID: "col_c", Name: "Done", Position: 2},
	}
	for _, column := range s.wanted {
		created, err := s.repo.CreateColumn(s.ctx, s.boardA.ID, column)
		if err != nil {
			t.Fatalf("CreateColumn(%s) failed: %v", column.ID, err)
		}
		if !reflect.DeepEqual(created, column) {
			t.Errorf("CreateColumn returned %+v, want %+v", created, column)
		}
	}

	columns, err = s.repo.GetColumns(s.ctx, s.boardA.ID)
	if err != nil {
		t.Fatalf("GetColumns failed: %v", err)
	}
	if !reflect.DeepEqual(columns, s.wanted) {
		t.Errorf("GetColumns returned %+v, want %+v", columns, s.wanted)
	}

	if _, err := s.repo.CreateColumn(s.ctx, s.boardA.ID, s.wanted[1]); !errors.Is(err, domain.ErrColumnExists) {
		t.Errorf("Expected ErrColumnExists on a duplicate column, got %v", err)
	}

	contractColumnsAreScoped(t, s)
}

// contractColumnsAreScoped checks that a column ID belongs to its board: two
// boards can both start with the same stages, and neither sees the other's.
func contractColumnsAreScoped(t *testing.T, s *contractState) {
	t.Helper()

	for _, column := range s.wanted {
		if _, err := s.repo.CreateColumn(s.ctx, s.boardB.ID, column); err != nil {
			t.Fatalf("CreateColumn(%s) on a second board failed: %v", column.ID, err)
		}
	}
	for _, board := range []domain.Board{s.boardA, s.boardB} {
		columns, err := s.repo.GetColumns(s.ctx, board.ID)
		if err != nil {
			t.Fatalf("GetColumns(%s) failed: %v", board.ID, err)
		}
		if !reflect.DeepEqual(columns, s.wanted) {
			t.Errorf("GetColumns(%s) returned %+v, want %+v", board.ID, columns, s.wanted)
		}
	}

	// Leave board B empty again for the phases that rely on that.
	for _, column := range s.wanted {
		if err := s.repo.DeleteColumn(s.ctx, s.boardB.ID, column.ID); err != nil {
			t.Fatalf("DeleteColumn(%s) on the second board failed: %v", column.ID, err)
		}
	}
}

func contractRenamesColumns(t *testing.T, s *contractState) {
	t.Helper()

	if err := s.repo.DeleteColumn(s.ctx, s.boardB.ID, "col_a"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected another board's column to be invisible, got %v", err)
	}

	// RenameColumn replaces a name in place: the column keeps its ID, its
	// position and its order.
	if err := s.repo.RenameColumn(s.ctx, s.boardB.ID, "col_a", "X"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected another board's column to be invisible to RenameColumn, got %v", err)
	}
	if err := s.repo.RenameColumn(s.ctx, s.boardA.ID, "missing", "X"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected ErrColumnNotFound on a rename of a missing column, got %v", err)
	}
	if err := s.repo.RenameColumn(s.ctx, s.boardA.ID, "col_b", "Work in progress"); err != nil {
		t.Errorf("RenameColumn failed: %v", err)
	}
	s.wanted[1].Name = "Work in progress"
	columns, err := s.repo.GetColumns(s.ctx, s.boardA.ID)
	if err != nil {
		t.Fatalf("GetColumns after RenameColumn failed: %v", err)
	}
	if !reflect.DeepEqual(columns, s.wanted) {
		t.Errorf("RenameColumn changed more than the name: got %+v, want %+v", columns, s.wanted)
	}
}

func contractCards(t *testing.T, s *contractState) {
	t.Helper()

	// A constraint target, so the dependency list has something to point at.
	dependency := domain.Card{ID: "contract_0", Title: "Foundation", ColumnID: "col_a", Priority: domain.PriorityHigh, Version: 1}
	if _, err := s.repo.Create(s.ctx, s.boardA.ID, dependency); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	s.card = domain.Card{
		ID:          "contract_1",
		Title:       "Contract",
		Description: "What it is",
		Objective:   "What done looks like",
		DependsOn:   []string{"contract_0"},
		ColumnID:    "col_b",
		Priority:    domain.PriorityMedium,
		Version:     1,
	}
	if _, err := s.repo.Create(s.ctx, s.boardA.ID, s.card); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	cards, err := s.repo.GetAll(s.ctx, s.boardA.ID)
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}
	if !containsCard(cards, s.card) {
		t.Fatalf("Expected the created card from GetAll, got %v", cards)
	}

	got, err := s.repo.GetByID(s.ctx, s.boardA.ID, s.card.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if !reflect.DeepEqual(got, s.card) {
		t.Errorf("GetByID did not return the created card: got %+v, want %+v", got, s.card)
	}
}

func contractCardReads(t *testing.T, s *contractState) {
	t.Helper()

	if _, err := s.repo.Create(s.ctx, s.boardA.ID, s.card); !errors.Is(err, domain.ErrCardExists) {
		t.Errorf("Expected ErrCardExists on a duplicate ID, got %v", err)
	}
	if _, err := s.repo.GetByID(s.ctx, s.boardA.ID, "missing"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound, got %v", err)
	}
	if _, err := s.repo.GetByID(s.ctx, s.boardB.ID, s.card.ID); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected another board's card to be invisible, got %v", err)
	}
}

func contractCardUpdates(t *testing.T, s *contractState) {
	t.Helper()

	// A full edit must persist every field, not just the column. An empty
	// (non-nil) constraint list explicitly clears the constraints.
	edited := domain.Card{
		ID:          s.card.ID,
		Title:       "Contract, edited",
		Description: "New description",
		Objective:   "New objective",
		DependsOn:   []string{},
		ColumnID:    "col_c",
		Priority:    domain.PriorityHigh,
		Version:     1,
	}
	if err := s.repo.Update(s.ctx, s.boardA.ID, edited); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	got, err := s.repo.GetByID(s.ctx, s.boardA.ID, s.card.ID)
	if err != nil {
		t.Fatalf("GetByID after Update failed: %v", err)
	}
	want := edited
	want.DependsOn = nil // clearing is stored as "no constraints"
	want.Version = 2     // storage advances the version it was given
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Update did not persist the whole card: got %+v, want %+v", got, want)
	}

	// A caller holding the previous version must be refused, not silently
	// overwrite the committed edit.
	stale := edited
	stale.Version = 1
	if err := s.repo.Update(s.ctx, s.boardA.ID, stale); !errors.Is(err, domain.ErrCardConflict) {
		t.Errorf("Expected ErrCardConflict for a stale version, got %v", err)
	}
}

func contractCardMoves(t *testing.T, s *contractState) {
	t.Helper()

	// A single-cell move must leave the rest of the card alone.
	if err := s.repo.UpdateColumn(s.ctx, s.boardA.ID, s.card.ID, "col_b"); err != nil {
		t.Fatalf("UpdateColumn failed: %v", err)
	}
	got, err := s.repo.GetByID(s.ctx, s.boardA.ID, s.card.ID)
	if err != nil {
		t.Fatalf("GetByID after UpdateColumn failed: %v", err)
	}
	if got.ColumnID != "col_b" || got.Title != "Contract, edited" || got.Objective != "New objective" {
		t.Errorf("UpdateColumn changed more than the column: %+v", got)
	}

	if err := s.repo.UpdateColumn(s.ctx, s.boardA.ID, "missing", "col_a"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound when moving an unknown card, got %v", err)
	}
	if err := s.repo.Update(s.ctx, s.boardA.ID, domain.Card{ID: "missing", Title: "Ghost", ColumnID: "col_a", Priority: domain.PriorityLow}); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound when updating an unknown card, got %v", err)
	}
}
