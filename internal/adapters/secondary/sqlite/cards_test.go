package sqlite

import (
	"context"
	"errors"
	"kanban-app/internal/domain"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSQLiteRepoCardCRUD(t *testing.T) {
	ctx := context.Background()
	repo := newBoardRepo(t, filepath.Join(t.TempDir(), "kanban.db"))
	createColumns(t, repo, domain.DefaultColumns()...)

	cards, err := repo.GetAll(ctx, testBoard)
	if err != nil {
		t.Fatalf("GetAll on a fresh database failed: %v", err)
	}
	if len(cards) != 0 {
		t.Fatalf("Expected an empty board, got %v", cards)
	}

	alpha := domain.Card{
		ID: "card_a", Title: "Alpha", Description: "What it is", Objective: "What done looks like",
		ColumnID: "To Do", Priority: domain.PriorityHigh, Version: 1,
	}
	if _, err := repo.Create(ctx, testBoard, alpha); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if _, err := repo.Create(ctx, testBoard, alpha); !errors.Is(err, domain.ErrCardExists) {
		t.Errorf("Expected ErrCardExists on a duplicate ID, got %v", err)
	}

	got, err := repo.GetByID(ctx, testBoard, "card_a")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if !reflect.DeepEqual(got, alpha) {
		t.Errorf("Round-trip mismatch: got %+v, want %+v", got, alpha)
	}

	if _, err := repo.GetByID(ctx, testBoard, "missing"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound, got %v", err)
	}
	testSQLiteCardMovesAndOrders(t, ctx, repo)
}

func testSQLiteCardMovesAndOrders(t *testing.T, ctx context.Context, repo *Repo) {
	t.Helper()

	if err := repo.UpdateColumn(ctx, testBoard, "card_a", "In Progress"); err != nil {
		t.Fatalf("UpdateColumn failed: %v", err)
	}
	if got, _ := repo.GetByID(ctx, testBoard, "card_a"); got.ColumnID != "In Progress" {
		t.Errorf("Expected the card in In Progress, got %q", got.ColumnID)
	}

	if err := repo.UpdateColumn(ctx, testBoard, "missing", "Done"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound for an unknown card, got %v", err)
	}

	beta := domain.Card{ID: "card_b", Title: "Beta", ColumnID: "In Progress", Priority: domain.PriorityLow}
	if _, err := repo.Create(ctx, testBoard, beta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	cards, err := repo.GetAll(ctx, testBoard)
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}
	if len(cards) != 2 || cards[0].ID != "card_a" || cards[1].ID != "card_b" {
		t.Errorf("Expected both cards in insertion order, got %+v", cards)
	}
}

func TestSQLiteRepoDeleteCardDropsBothDirections(t *testing.T) {
	ctx := context.Background()
	repo := newBoardRepo(t, filepath.Join(t.TempDir(), "kanban.db"))
	createColumns(t, repo, domain.DefaultColumns()...)

	if _, err := repo.Create(ctx, testBoard, domain.Card{ID: "target", Title: "Target", ColumnID: "To Do", Priority: domain.PriorityLow}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if _, err := repo.Create(ctx, testBoard, domain.Card{
		ID: "dependent", Title: "Dependent", ColumnID: "To Do",
		DependsOn: []string{"target"}, Priority: domain.PriorityLow,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	// The dependency row is the only trace of target outside its card row.
	if got, _ := repo.GetByID(ctx, testBoard, "dependent"); !reflect.DeepEqual(got.DependsOn, []string{"target"}) {
		t.Fatalf("Setup failed: %+v", got)
	}

	if err := repo.DeleteCard(ctx, testBoard, "target"); err != nil {
		t.Fatalf("DeleteCard failed: %v", err)
	}
	if got, _ := repo.GetByID(ctx, testBoard, "dependent"); got.DependsOn != nil {
		t.Errorf("Expected the constraint dropped, got %v", got.DependsOn)
	}
	if _, err := repo.GetByID(ctx, testBoard, "target"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected the deleted card to be gone, got %v", err)
	}
	if err := repo.DeleteCard(ctx, testBoard, "target"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound on a second delete, got %v", err)
	}
}

func TestSQLiteRepoRejectsUnknownDependency(t *testing.T) {
	ctx := context.Background()
	repo := newBoardRepo(t, filepath.Join(t.TempDir(), "kanban.db"))
	createColumns(t, repo, domain.DefaultColumns()...)
	testSQLiteRejectsUnknownOnCreate(t, ctx, repo)
	testSQLiteRejectsUnknownOnUpdate(t, ctx, repo)
}

func testSQLiteRejectsUnknownOnCreate(t *testing.T, ctx context.Context, repo *Repo) {
	t.Helper()

	_, err := repo.Create(ctx, testBoard, domain.Card{
		ID: "card_a", Title: "Depends on a ghost",
		DependsOn: []string{"ghost"},
		ColumnID:  "To Do", Priority: domain.PriorityLow,
	})
	if !errors.Is(err, domain.ErrUnknownDependency) {
		t.Errorf("Expected ErrUnknownDependency, got %v", err)
	}

	// The failed insert must not leave a half-written card behind.
	if _, err := repo.GetByID(ctx, testBoard, "card_a"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected the transaction to roll back, got %v", err)
	}
}

// testSQLiteRejectsUnknownOnUpdate checks the same rule on an edit: the
// previous constraints must survive the failed transaction.
func testSQLiteRejectsUnknownOnUpdate(t *testing.T, ctx context.Context, repo *Repo) {
	t.Helper()

	for _, id := range []string{"card_b", "card_c"} {
		if _, err := repo.Create(ctx, testBoard, domain.Card{ID: id, Title: id, ColumnID: "To Do", Priority: domain.PriorityLow}); err != nil {
			t.Fatalf("Create %s failed: %v", id, err)
		}
	}
	card, err := repo.GetByID(ctx, testBoard, "card_b")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	card.DependsOn = []string{"card_c"}
	if err := repo.Update(ctx, testBoard, card); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	card, _ = repo.GetByID(ctx, testBoard, "card_b")
	card.DependsOn = []string{"ghost"}
	if err := repo.Update(ctx, testBoard, card); !errors.Is(err, domain.ErrUnknownDependency) {
		t.Errorf("Expected ErrUnknownDependency on Update, got %v", err)
	}
	if got, _ := repo.GetByID(ctx, testBoard, "card_b"); !reflect.DeepEqual(got.DependsOn, []string{"card_c"}) {
		t.Errorf("Expected the rolled-back constraints to survive, got %v", got.DependsOn)
	}
}

func TestSQLiteRepoUpdateReplacesDependencies(t *testing.T) {
	ctx := context.Background()
	repo := newBoardRepo(t, filepath.Join(t.TempDir(), "kanban.db"))
	createColumns(t, repo, domain.DefaultColumns()...)

	for _, id := range []string{"card_a", "card_b", "card_c"} {
		if _, err := repo.Create(ctx, testBoard, domain.Card{ID: id, Title: id, ColumnID: "To Do", Priority: domain.PriorityLow}); err != nil {
			t.Fatalf("Create %s failed: %v", id, err)
		}
	}
	testSQLiteDependenciesAreReplaced(t, ctx, repo)
}

func testSQLiteDependenciesAreReplaced(t *testing.T, ctx context.Context, repo *Repo) {
	t.Helper()

	card, _ := repo.GetByID(ctx, testBoard, "card_a")
	card.DependsOn = []string{"card_b", "card_c"}
	if err := repo.Update(ctx, testBoard, card); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if got, _ := repo.GetByID(ctx, testBoard, "card_a"); !reflect.DeepEqual(got.DependsOn, []string{"card_b", "card_c"}) {
		t.Errorf("Expected both constraints, got %v", got.DependsOn)
	}

	card, _ = repo.GetByID(ctx, testBoard, "card_a")
	card.DependsOn = []string{"card_c"}
	if err := repo.Update(ctx, testBoard, card); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if got, _ := repo.GetByID(ctx, testBoard, "card_a"); !reflect.DeepEqual(got.DependsOn, []string{"card_c"}) {
		t.Errorf("Expected only card_c to remain, got %v", got.DependsOn)
	}

	// GetAll must carry the dependencies too.
	cards, err := repo.GetAll(ctx, testBoard)
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}
	for _, c := range cards {
		if c.ID != "card_a" {
			continue
		}
		if !reflect.DeepEqual(c.DependsOn, []string{"card_c"}) {
			t.Errorf("GetAll lost the dependencies: %v", c.DependsOn)
		}
	}
}

func TestSQLiteRepoUnknownBoardIsAPerBoardError(t *testing.T) {
	ctx := context.Background()
	repo := newBoardRepo(t, filepath.Join(t.TempDir(), "kanban.db"))
	createColumns(t, repo, domain.DefaultColumns()...)
	if _, err := repo.Create(ctx, testBoard, domain.Card{ID: "card_a", Title: "Alpha", ColumnID: "To Do", Priority: domain.PriorityLow}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if _, err := repo.GetAll(ctx, "ghost"); !errors.Is(err, domain.ErrBoardNotFound) {
		t.Errorf("Expected ErrBoardNotFound for an unknown board, got %v", err)
	}
	if _, err := repo.CreateBoard(ctx, domain.Board{ID: testBoard, Name: "Again"}); !errors.Is(err, domain.ErrBoardExists) {
		t.Errorf("Expected ErrBoardExists for a duplicate ID, got %v", err)
	}
}

func TestSQLiteRepoDegradesHandEditedValues(t *testing.T) {
	ctx := context.Background()
	repo := newBoardRepo(t, filepath.Join(t.TempDir(), "kanban.db"))
	createColumns(t, repo, domain.DefaultColumns()...)

	// Simulate a row edited outside the application: unknown priority, and a
	// column the board no longer has.
	if _, err := repo.db.ExecContext(ctx,
		`INSERT INTO cards (id, title, status, priority, board_id) VALUES (?, ?, ?, ?, ?)`,
		"hand_edited", "Edited by hand", "Removed column", "Urgent", testBoard); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	got, err := repo.GetByID(ctx, testBoard, "hand_edited")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got.Priority != domain.PriorityLow {
		t.Errorf("Expected an invalid priority to degrade to Low, got %q", got.Priority)
	}
	// Storage keeps the unknown column: putting the card back on the board is
	// the use case's job.
	if got.ColumnID != "Removed column" {
		t.Errorf("Expected the stored column to be kept, got %q", got.ColumnID)
	}
}

func TestSQLiteRepoUpdateSavesEveryField(t *testing.T) {
	ctx := context.Background()
	repo := newBoardRepo(t, filepath.Join(t.TempDir(), "kanban.db"))
	createColumns(t, repo, domain.DefaultColumns()...)

	original := domain.Card{ID: "card_1", Title: "Title", ColumnID: "To Do", Priority: domain.PriorityLow, Version: 1}
	if _, err := repo.Create(ctx, testBoard, original); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	edited := domain.Card{
		ID:          "card_1",
		Title:       "Edited title",
		Description: "Description",
		Objective:   "Objective",
		ColumnID:    "Done",
		Priority:    domain.PriorityHigh,
		Version:     1, // the version the edit started from
	}
	if err := repo.Update(ctx, testBoard, edited); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	got, err := repo.GetByID(ctx, testBoard, "card_1")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	edited.Version = 2 // storage advances it atomically
	if !reflect.DeepEqual(got, edited) {
		t.Errorf("Update did not persist the whole card: got %+v, want %+v", got, edited)
	}

	if err := repo.Update(ctx, testBoard, domain.Card{ID: "missing", Title: "Ghost", ColumnID: "To Do", Priority: domain.PriorityLow}); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound, got %v", err)
	}
}
