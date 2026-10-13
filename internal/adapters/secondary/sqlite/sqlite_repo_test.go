package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"kanban-app/internal/domain"
)

// testBoard is the workspace the repo tests use; each fresh database gets one.
const testBoard = "bda"

func newRepo(t *testing.T, path string) *Repo {
	t.Helper()
	repo, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	return repo
}

func newBoardRepo(t *testing.T, path string) *Repo {
	t.Helper()
	repo := newRepo(t, path)
	if _, err := repo.CreateBoard(context.Background(), domain.Board{ID: testBoard, Name: "Test board"}); err != nil {
		t.Fatalf("CreateBoard failed: %v", err)
	}
	return repo
}

func createColumns(t *testing.T, repo *Repo, columns ...domain.Column) {
	t.Helper()
	for _, column := range columns {
		if _, err := repo.CreateColumn(context.Background(), testBoard, column); err != nil {
			t.Fatalf("CreateColumn(%s) failed: %v", column.ID, err)
		}
	}
}

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
		ColumnID: "To Do", Priority: domain.PriorityHigh,
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
	cards, err = repo.GetAll(ctx, testBoard)
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}
	if len(cards) != 2 || cards[0].ID != "card_a" || cards[1].ID != "card_b" {
		t.Errorf("Expected both cards in insertion order, got %+v", cards)
	}
}

func TestSQLiteRepoColumns(t *testing.T) {
	ctx := context.Background()
	repo := newBoardRepo(t, filepath.Join(t.TempDir(), "kanban.db"))

	columns, err := repo.GetColumns(ctx, testBoard)
	if err != nil {
		t.Fatalf("GetColumns failed: %v", err)
	}
	if len(columns) != 0 {
		t.Fatalf("Expected no column on a fresh database, got %v", columns)
	}

	createColumns(t, repo,
		domain.Column{ID: "col_a", Name: "To Do", Position: 0},
		domain.Column{ID: "col_b", Name: "Doing", Position: 1},
		domain.Column{ID: "col_c", Name: "Done", Position: 2},
	)

	columns, err = repo.GetColumns(ctx, testBoard)
	if err != nil {
		t.Fatalf("GetColumns failed: %v", err)
	}
	if len(columns) != 3 || columns[0].ID != "col_a" || columns[2].ID != "col_c" {
		t.Fatalf("Expected the three columns in order, got %+v", columns)
	}

	if _, err := repo.CreateColumn(ctx, testBoard, domain.Column{ID: "col_b", Name: "Duplicate", Position: 3}); !errors.Is(err, domain.ErrColumnExists) {
		t.Errorf("Expected ErrColumnExists, got %v", err)
	}

	// Deleting a column closes the gap: the next column created must land after
	// the survivors, never on a duplicate position.
	if err := repo.DeleteColumn(ctx, testBoard, "col_b"); err != nil {
		t.Fatalf("DeleteColumn failed: %v", err)
	}
	columns, err = repo.GetColumns(ctx, testBoard)
	if err != nil {
		t.Fatalf("GetColumns failed: %v", err)
	}
	want := []domain.Column{
		{ID: "col_a", Name: "To Do", Position: 0},
		{ID: "col_c", Name: "Done", Position: 1},
	}
	if !reflect.DeepEqual(columns, want) {
		t.Fatalf("Expected the survivors renumbered, got %+v", columns)
	}

	if _, err := repo.CreateColumn(ctx, testBoard, domain.Column{ID: "col_d", Name: "Review", Position: len(columns)}); err != nil {
		t.Fatalf("CreateColumn failed: %v", err)
	}
	columns, _ = repo.GetColumns(ctx, testBoard)
	if len(columns) != 3 || columns[2].ID != "col_d" || columns[2].Position != 2 {
		t.Errorf("Expected the new column last, got %+v", columns)
	}

	if err := repo.DeleteColumn(ctx, testBoard, "missing"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Expected ErrColumnNotFound, got %v", err)
	}
}

func TestSQLiteRepoPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kanban.db")

	open := func() *Repo {
		repo, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		return repo
	}
	probe := open()
	t.Cleanup(func() { probe.Close() })
	if _, err := probe.CreateBoard(ctx, domain.Board{ID: testBoard, Name: "Test board"}); err != nil {
		t.Fatalf("CreateBoard failed: %v", err)
	}

	first := open()
	t.Cleanup(func() { first.Close() })
	createColumns(t, first,
		domain.Column{ID: "col_a", Name: "To Do", Position: 0},
		domain.Column{ID: "col_b", Name: "Doing", Position: 1},
	)
	if _, err := first.Create(ctx, testBoard, domain.Card{ID: "foundation", Title: "Foundation", ColumnID: "col_b", Priority: domain.PriorityLow}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if _, err := first.Create(ctx, testBoard, domain.Card{
		ID: "keep_me", Title: "Survives restart", Description: "With content",
		DependsOn: []string{"foundation"},
		ColumnID:  "col_a", Priority: domain.PriorityMedium,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := first.UpdateColumn(ctx, testBoard, "keep_me", "col_b"); err != nil {
		t.Fatalf("UpdateColumn failed: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	second := open()
	t.Cleanup(func() { second.Close() })
	got, err := second.GetByID(ctx, testBoard, "keep_me")
	if err != nil {
		t.Fatalf("GetByID after reopen failed: %v", err)
	}
	if got.ColumnID != "col_b" || got.Title != "Survives restart" || got.Description != "With content" {
		t.Errorf("Card changed across reopen: %+v", got)
	}
	if !reflect.DeepEqual(got.DependsOn, []string{"foundation"}) {
		t.Errorf("Constraints did not survive the reopen: %v", got.DependsOn)
	}

	columns, err := second.GetColumns(ctx, testBoard)
	if err != nil {
		t.Fatalf("GetColumns after reopen failed: %v", err)
	}
	if len(columns) != 2 || columns[0].Name != "To Do" || columns[1].Name != "Doing" {
		t.Errorf("Columns did not survive the reopen: %+v", columns)
	}
}

// TestSQLiteRepoUnknownBoardIsAPerBoardError covers the scoping the core relies
// on: an unknown board is refused like an unknown column, and a duplicate board
// is rejected.
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

// TestSQLiteRepoDeleteCardDropsBothDirections covers the two FK cascades: the
// deleted card's own constraints and the constraints pointing at it disappear,
// so no dependent keeps a ghost.
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

// TestSQLiteRepoRejectsUnknownDependency covers the foreign key on the
// dependency table: a constraint must point at an existing card.
func TestSQLiteRepoRejectsUnknownDependency(t *testing.T) {
	ctx := context.Background()
	repo := newBoardRepo(t, filepath.Join(t.TempDir(), "kanban.db"))
	createColumns(t, repo, domain.DefaultColumns()...)

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

	// Same rule on an edit, and the previous constraints must survive the
	// failed transaction.
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

	card.DependsOn = []string{"ghost"}
	if err := repo.Update(ctx, testBoard, card); !errors.Is(err, domain.ErrUnknownDependency) {
		t.Errorf("Expected ErrUnknownDependency on Update, got %v", err)
	}
	if got, _ := repo.GetByID(ctx, testBoard, "card_b"); !reflect.DeepEqual(got.DependsOn, []string{"card_c"}) {
		t.Errorf("Expected the rolled-back constraints to survive, got %v", got.DependsOn)
	}
}

// TestSQLiteRepoUpdateReplacesDependencies checks that an edit both adds and
// removes constraints.
func TestSQLiteRepoUpdateReplacesDependencies(t *testing.T) {
	ctx := context.Background()
	repo := newBoardRepo(t, filepath.Join(t.TempDir(), "kanban.db"))
	createColumns(t, repo, domain.DefaultColumns()...)

	for _, id := range []string{"card_a", "card_b", "card_c"} {
		if _, err := repo.Create(ctx, testBoard, domain.Card{ID: id, Title: id, ColumnID: "To Do", Priority: domain.PriorityLow}); err != nil {
			t.Fatalf("Create %s failed: %v", id, err)
		}
	}

	card, _ := repo.GetByID(ctx, testBoard, "card_a")
	card.DependsOn = []string{"card_b", "card_c"}
	if err := repo.Update(ctx, testBoard, card); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if got, _ := repo.GetByID(ctx, testBoard, "card_a"); !reflect.DeepEqual(got.DependsOn, []string{"card_b", "card_c"}) {
		t.Errorf("Expected both constraints, got %v", got.DependsOn)
	}

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

	original := domain.Card{ID: "card_1", Title: "Title", ColumnID: "To Do", Priority: domain.PriorityLow}
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
	}
	if err := repo.Update(ctx, testBoard, edited); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	got, err := repo.GetByID(ctx, testBoard, "card_1")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if !reflect.DeepEqual(got, edited) {
		t.Errorf("Update did not persist the whole card: got %+v, want %+v", got, edited)
	}

	if err := repo.Update(ctx, testBoard, domain.Card{ID: "missing", Title: "Ghost", ColumnID: "To Do", Priority: domain.PriorityLow}); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound, got %v", err)
	}
}

// TestSQLiteRepoMigratesAnOlderDatabase checks that a kanban.db created before
// the content columns, the editable columns and the boards existed is upgraded
// in place, without losing cards or their column — the legacy content lands in
// the default board.
func TestSQLiteRepoMigratesAnOlderDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kanban.db")

	legacy, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatalf("Opening the legacy database failed: %v", err)
	}
	// The schema of an earlier release: free-text constraints, no description
	// and objective, no columns and no boards table. The status column held the
	// stage.
	if _, err := legacy.ExecContext(ctx, `CREATE TABLE cards (
		id TEXT PRIMARY KEY, title TEXT NOT NULL, constraints TEXT,
		status TEXT NOT NULL, priority TEXT NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("Creating the legacy schema failed: %v", err)
	}
	if _, err := legacy.ExecContext(ctx,
		`INSERT INTO cards (id, title, status, priority) VALUES (?, ?, ?, ?)`,
		"old_card", "Written by the previous version", "Done", "High"); err != nil {
		t.Fatalf("Seeding the legacy database failed: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("Closing the legacy database failed: %v", err)
	}

	repo := newRepo(t, path)
	t.Cleanup(func() { repo.Close() })

	// The migration invented the default board; the card went there.
	boards, err := repo.ListBoards(ctx)
	if err != nil {
		t.Fatalf("ListBoards on a migrated database failed: %v", err)
	}
	if len(boards) != 1 || boards[0].ID != defaultBoardID {
		t.Fatalf("Expected the default board to be created, got %+v", boards)
	}

	got, err := repo.GetByID(ctx, defaultBoardID, "old_card")
	if err != nil {
		t.Fatalf("The existing card did not survive the migration: %v", err)
	}
	if got.Title != "Written by the previous version" || got.ColumnID != "Done" {
		t.Errorf("Existing values changed during the migration: %+v", got)
	}
	if len(got.DependsOn) != 0 {
		t.Errorf("Expected no constraint after the migration, got %v", got.DependsOn)
	}

	// The columns table is created by the schema, so the board can be rebuilt
	// with the default stages: the historical IDs match the old status values.
	columns, err := repo.GetColumns(ctx, defaultBoardID)
	if err != nil {
		t.Fatalf("GetColumns on a migrated database failed: %v", err)
	}
	if len(columns) != 0 {
		t.Fatalf("Expected an empty columns table, got %+v", columns)
	}
	for _, column := range domain.DefaultColumns() {
		if _, err := repo.CreateColumn(ctx, defaultBoardID, column); err != nil {
			t.Fatalf("CreateColumn(%s) failed: %v", column.ID, err)
		}
	}
	if got, _ := repo.GetByID(ctx, defaultBoardID, "old_card"); got.ColumnID != "Done" {
		t.Errorf("Expected the card to still point at Done, got %q", got.ColumnID)
	}

	// The migrated database must accept the new fields.
	edited := got
	edited.Description = "Now editable"
	if err := repo.Update(ctx, defaultBoardID, edited); err != nil {
		t.Fatalf("Update on a migrated database failed: %v", err)
	}
	if again, _ := repo.GetByID(ctx, defaultBoardID, "old_card"); again.Description != "Now editable" {
		t.Errorf("Description not stored after the migration: %+v", again)
	}
}

func TestOpenRequiresAPath(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Error("Expected an error for an empty database path")
	}
}
