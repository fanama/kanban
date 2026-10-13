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

func contract(t *testing.T, repo ports.BoardRepository) {
	t.Helper()
	ctx := context.Background()

	// --- boards -------------------------------------------------------------

	boards, err := repo.ListBoards(ctx)
	if err != nil {
		t.Fatalf("ListBoards failed: %v", err)
	}
	if len(boards) != 0 {
		t.Fatalf("Expected no board yet, got %v", boards)
	}

	boardA := domain.Board{ID: "bda", Name: "Board A"}
	if _, err := repo.CreateBoard(ctx, boardA); err != nil {
		t.Fatalf("CreateBoard failed: %v", err)
	}
	boardB := domain.Board{ID: "bdb", Name: "Board B"}
	if _, err := repo.CreateBoard(ctx, boardB); err != nil {
		t.Fatalf("CreateBoard failed: %v", err)
	}
	if _, err := repo.CreateBoard(ctx, domain.Board{ID: "bda", Name: "Other"}); !errors.Is(err, domain.ErrBoardExists) {
		t.Errorf("Expected ErrBoardExists on a duplicate board, got %v", err)
	}

	// --- columns ------------------------------------------------------------

	columns, err := repo.GetColumns(ctx, boardA.ID)
	if err != nil {
		t.Fatalf("GetColumns failed: %v", err)
	}
	if len(columns) != 0 {
		t.Fatalf("Expected an empty board, got %v", columns)
	}

	wanted := []domain.Column{
		{ID: "col_a", Name: "To Do", Position: 0},
		{ID: "col_b", Name: "Doing", Position: 1},
		{ID: "col_c", Name: "Done", Position: 2},
	}
	for _, column := range wanted {
		created, err := repo.CreateColumn(ctx, boardA.ID, column)
		if err != nil {
			t.Fatalf("CreateColumn(%s) failed: %v", column.ID, err)
		}
		if !reflect.DeepEqual(created, column) {
			t.Errorf("CreateColumn returned %+v, want %+v", created, column)
		}
	}

	columns, err = repo.GetColumns(ctx, boardA.ID)
	if err != nil {
		t.Fatalf("GetColumns failed: %v", err)
	}
	if !reflect.DeepEqual(columns, wanted) {
		t.Errorf("GetColumns returned %+v, want %+v", columns, wanted)
	}

	if _, err := repo.CreateColumn(ctx, boardA.ID, wanted[1]); !errors.Is(err, domain.ErrColumnExists) {
		t.Errorf("Expected ErrColumnExists on a duplicate column, got %v", err)
	}

	if err := repo.DeleteColumn(ctx, boardB.ID, "col_a"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected another board's column to be invisible, got %v", err)
	}

	// RenameColumn replaces a name in place: the column keeps its ID, its
	// position and its order.
	if err := repo.RenameColumn(ctx, boardB.ID, "col_a", "X"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected another board's column to be invisible to RenameColumn, got %v", err)
	}
	if err := repo.RenameColumn(ctx, boardA.ID, "missing", "X"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected ErrColumnNotFound on a rename of a missing column, got %v", err)
	}
	if err := repo.RenameColumn(ctx, boardA.ID, "col_b", "Work in progress"); err != nil {
		t.Errorf("RenameColumn failed: %v", err)
	}
	wanted[1].Name = "Work in progress"
	columns, err = repo.GetColumns(ctx, boardA.ID)
	if err != nil {
		t.Fatalf("GetColumns after RenameColumn failed: %v", err)
	}
	if !reflect.DeepEqual(columns, wanted) {
		t.Errorf("RenameColumn changed more than the name: got %+v, want %+v", columns, wanted)
	}

	// --- cards --------------------------------------------------------------

	// A constraint target, so the dependency list has something to point at.
	dependency := domain.Card{ID: "contract_0", Title: "Foundation", ColumnID: "col_a", Priority: domain.PriorityHigh}
	if _, err := repo.Create(ctx, boardA.ID, dependency); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	card := domain.Card{
		ID:          "contract_1",
		Title:       "Contract",
		Description: "What it is",
		Objective:   "What done looks like",
		DependsOn:   []string{"contract_0"},
		ColumnID:    "col_b",
		Priority:    domain.PriorityMedium,
	}
	if _, err := repo.Create(ctx, boardA.ID, card); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	cards, err := repo.GetAll(ctx, boardA.ID)
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}
	if !containsCard(cards, card) {
		t.Fatalf("Expected the created card from GetAll, got %v", cards)
	}

	got, err := repo.GetByID(ctx, boardA.ID, card.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if !reflect.DeepEqual(got, card) {
		t.Errorf("GetByID did not return the created card: got %+v, want %+v", got, card)
	}

	if _, err := repo.Create(ctx, boardA.ID, card); !errors.Is(err, domain.ErrCardExists) {
		t.Errorf("Expected ErrCardExists on a duplicate ID, got %v", err)
	}

	if _, err := repo.GetByID(ctx, boardA.ID, "missing"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound, got %v", err)
	}
	if _, err := repo.GetByID(ctx, boardB.ID, card.ID); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected another board's card to be invisible, got %v", err)
	}

	// A full edit must persist every field, not just the column. An empty
	// (non-nil) constraint list explicitly clears the constraints.
	edited := domain.Card{
		ID:          card.ID,
		Title:       "Contract, edited",
		Description: "New description",
		Objective:   "New objective",
		DependsOn:   []string{},
		ColumnID:    "col_c",
		Priority:    domain.PriorityHigh,
	}
	if err := repo.Update(ctx, boardA.ID, edited); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	got, err = repo.GetByID(ctx, boardA.ID, card.ID)
	if err != nil {
		t.Fatalf("GetByID after Update failed: %v", err)
	}
	want := edited
	want.DependsOn = nil // clearing is stored as "no constraints"
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Update did not persist the whole card: got %+v, want %+v", got, want)
	}

	// A single-cell move must leave the rest of the card alone.
	if err := repo.UpdateColumn(ctx, boardA.ID, card.ID, "col_b"); err != nil {
		t.Fatalf("UpdateColumn failed: %v", err)
	}
	got, err = repo.GetByID(ctx, boardA.ID, card.ID)
	if err != nil {
		t.Fatalf("GetByID after UpdateColumn failed: %v", err)
	}
	if got.ColumnID != "col_b" || got.Title != "Contract, edited" || got.Objective != "New objective" {
		t.Errorf("UpdateColumn changed more than the column: %+v", got)
	}

	if err := repo.UpdateColumn(ctx, boardA.ID, "missing", "col_a"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound when moving an unknown card, got %v", err)
	}

	if err := repo.Update(ctx, boardA.ID, domain.Card{ID: "missing", Title: "Ghost", ColumnID: "col_a", Priority: domain.PriorityLow}); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound when updating an unknown card, got %v", err)
	}

	// --- deleting a card ----------------------------------------------------

	// contract_1 depends on contract_0: deleting the constraint drops it from
	// the dependent's list instead of leaving a ghost.
	if err := repo.DeleteCard(ctx, boardA.ID, "contract_0"); err != nil {
		t.Fatalf("DeleteCard failed: %v", err)
	}
	if _, err := repo.GetByID(ctx, boardA.ID, "contract_0"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected the deleted card to be gone, got %v", err)
	}
	got, err = repo.GetByID(ctx, boardA.ID, card.ID)
	if err != nil {
		t.Fatalf("GetByID after DeleteCard failed: %v", err)
	}
	if len(got.DependsOn) != 0 {
		t.Errorf("Expected the constraint to be dropped from the dependent, got %v", got.DependsOn)
	}
	if err := repo.DeleteCard(ctx, boardA.ID, "missing"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound when deleting twice, got %v", err)
	}

	// --- deleting a column --------------------------------------------------

	// Deleting renumbers the survivors, so the next column created lands after
	// them and the ordering stays dense.
	if err := repo.DeleteColumn(ctx, boardA.ID, "col_b"); err != nil {
		t.Fatalf("DeleteColumn failed: %v", err)
	}
	columns, err = repo.GetColumns(ctx, boardA.ID)
	if err != nil {
		t.Fatalf("GetColumns after DeleteColumn failed: %v", err)
	}
	if !reflect.DeepEqual(columns, []domain.Column{wanted[0], {ID: "col_c", Name: "Done", Position: 1}}) {
		t.Errorf("Expected the survivors renumbered, got %+v", columns)
	}

	// The cards of a deleted column are the use case's business: storage must
	// not touch them.
	got, err = repo.GetByID(ctx, boardA.ID, card.ID)
	if err != nil {
		t.Fatalf("GetByID after DeleteColumn failed: %v", err)
	}
	if got.ColumnID != "col_b" || got.Title != "Contract, edited" {
		t.Errorf("DeleteColumn must not touch the cards, got %+v", got)
	}

	if err := repo.DeleteColumn(ctx, boardA.ID, "missing"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected ErrColumnNotFound, got %v", err)
	}

	// --- replacing the whole board (board file import) -----------------------

	importedColumns := []domain.Column{
		{ID: "col_backlog", Name: "Backlog", Position: 0},
		{ID: "col_wip", Name: "WIP", Position: 1},
	}
	importedCards := []domain.Card{
		{ID: "imp_0", Title: "Seed", ColumnID: "col_backlog", Priority: domain.PriorityLow},
		{ID: "imp_1", Title: "Imported", Description: "From a file", ColumnID: "col_wip",
			DependsOn: []string{"imp_0"}, Priority: domain.PriorityHigh},
	}
	if err := repo.ReplaceBoard(ctx, boardA.ID, importedColumns, importedCards); err != nil {
		t.Fatalf("ReplaceBoard failed: %v", err)
	}

	columns, err = repo.GetColumns(ctx, boardA.ID)
	if err != nil {
		t.Fatalf("GetColumns after ReplaceBoard failed: %v", err)
	}
	if !reflect.DeepEqual(columns, importedColumns) {
		t.Errorf("Expected the imported columns, got %+v", columns)
	}
	for _, want := range importedCards {
		got, err := repo.GetByID(ctx, boardA.ID, want.ID)
		if err != nil {
			t.Fatalf("GetByID(%s) failed: %v", want.ID, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("GetByID(%s) returned %+v, want %+v", want.ID, got, want)
		}
	}

	// Everything the board used to hold is gone.
	if _, err := repo.GetByID(ctx, boardA.ID, card.ID); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected the old cards to be gone, got %v", err)
	}
	if err := repo.DeleteColumn(ctx, boardA.ID, "col_a"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected the old columns to be gone, got %v", err)
	}

	// --- board replacement is scoped, and DeleteBoard takes everything ------

	// Board B still holds whatever it had — here, nothing: the import of A
	// must not have reached it.
	bCards, err := repo.GetAll(ctx, boardB.ID)
	if err != nil {
		t.Fatalf("GetAll on board B failed: %v", err)
	}
	if len(bCards) != 0 {
		t.Errorf("Expected the import to stay inside board A, got %v", bCards)
	}

	if err := repo.DeleteBoard(ctx, boardB.ID); err != nil {
		t.Fatalf("DeleteBoard failed: %v", err)
	}
	if _, err := repo.GetColumns(ctx, boardB.ID); !errors.Is(err, domain.ErrBoardNotFound) {
		t.Errorf("Expected the deleted board to be gone, got %v", err)
	}
	boards, err = repo.ListBoards(ctx)
	if err != nil {
		t.Fatalf("ListBoards failed: %v", err)
	}
	if len(boards) != 1 || boards[0].ID != boardA.ID {
		t.Errorf("Expected only board A to remain, got %+v", boards)
	}

	// Board A keeps working after its sibling is gone.
	if err := repo.DeleteBoard(ctx, boardA.ID); err != nil {
		t.Fatalf("DeleteBoard failed: %v", err)
	}
	if _, err := repo.GetAll(ctx, boardA.ID); !errors.Is(err, domain.ErrBoardNotFound) {
		t.Errorf("Expected deleting a board to take its cards, got %v", err)
	}
	boards, err = repo.ListBoards(ctx)
	if err != nil {
		t.Fatalf("ListBoards failed: %v", err)
	}
	if len(boards) != 0 {
		t.Errorf("Expected an empty list of boards, got %+v", boards)
	}

	if err := repo.DeleteBoard(ctx, "missing"); !errors.Is(err, domain.ErrBoardNotFound) {
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
