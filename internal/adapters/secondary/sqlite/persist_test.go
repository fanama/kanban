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

func TestSQLiteRepoPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kanban.db")
	seedPersistedDatabase(t, ctx, path)
	verifyPersistedDatabase(t, ctx, path)
}

func seedPersistedDatabase(t *testing.T, ctx context.Context, path string) {
	t.Helper()

	probe, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { probe.Close() })
	if _, err := probe.CreateBoard(ctx, domain.Board{ID: testBoard, Name: "Test board"}); err != nil {
		t.Fatalf("CreateBoard failed: %v", err)
	}

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
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
}

func verifyPersistedDatabase(t *testing.T, ctx context.Context, path string) {
	t.Helper()

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
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

// TestSQLiteRepoMigratesAnOlderDatabase checks that a kanban.db created before
// the content columns, the editable columns and the boards existed is upgraded
// in place, without losing cards or their column — the legacy content lands in
// the default board.
func TestSQLiteRepoMigratesAnOlderDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kanban.db")
	createLegacyDatabase(t, ctx, path)

	repo := newRepo(t, path)
	t.Cleanup(func() { repo.Close() })
	testMigratedBoard(t, ctx, repo)
	testMigratedColumns(t, ctx, repo)
	testMigratedEdits(t, ctx, repo)
}

// createLegacyDatabase writes the schema of an earlier release: free-text
// constraints, no description and objective, no columns and no boards table.
// The status column held the stage.
func createLegacyDatabase(t *testing.T, ctx context.Context, path string) {
	t.Helper()

	legacy, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatalf("Opening the legacy database failed: %v", err)
	}
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
}

func testMigratedBoard(t *testing.T, ctx context.Context, repo *Repo) {
	t.Helper()

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
}

func testMigratedColumns(t *testing.T, ctx context.Context, repo *Repo) {
	t.Helper()

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
}

// testMigratedEdits checks that the migrated database accepts the new fields.
func testMigratedEdits(t *testing.T, ctx context.Context, repo *Repo) {
	t.Helper()

	got, err := repo.GetByID(ctx, defaultBoardID, "old_card")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	got.Description = "Now editable"
	if err := repo.Update(ctx, defaultBoardID, got); err != nil {
		t.Fatalf("Update on a migrated database failed: %v", err)
	}
	if again, _ := repo.GetByID(ctx, defaultBoardID, "old_card"); again.Description != "Now editable" {
		t.Errorf("Description not stored after the migration: %+v", again)
	}
}

// TestSQLiteRepoMigratesGlobalColumnKeys checks that a file written when a
// column's ID was globally unique is rebuilt so each board owns its stages: the
// existing rows survive and a second board can start with the same "To Do".
func TestSQLiteRepoMigratesGlobalColumnKeys(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kanban.db")
	createGlobalColumnKeyDatabase(t, ctx, path)

	repo := newRepo(t, path)
	t.Cleanup(func() { repo.Close() })
	testMigratedColumnKeys(t, ctx, repo)
}

// createGlobalColumnKeyDatabase writes the schema of a release that scoped
// cards and columns to a board but still keyed the column ID globally.
func createGlobalColumnKeyDatabase(t *testing.T, ctx context.Context, path string) {
	t.Helper()

	old, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatalf("Opening the old database failed: %v", err)
	}
	statements := []string{
		`CREATE TABLE boards (id TEXT PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE cards (
			id TEXT PRIMARY KEY, title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '', objective TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL, priority TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			board_id TEXT NOT NULL DEFAULT 'default')`,
		`CREATE TABLE card_dependencies (
			card_id TEXT NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
			depends_on_id TEXT NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
			position INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (card_id, depends_on_id))`,
		`CREATE TABLE columns (
			id TEXT PRIMARY KEY, name TEXT NOT NULL,
			position INTEGER NOT NULL, board_id TEXT NOT NULL DEFAULT 'default')`,
		`INSERT INTO boards (id, name) VALUES ('default', 'My board')`,
		`INSERT INTO columns (id, name, position, board_id) VALUES ('To Do', 'To Do', 0, 'default')`,
		`INSERT INTO cards (id, title, status, priority, board_id)
		 VALUES ('old_card', 'Kept by the migration', 'To Do', 'Low', 'default')`,
	}
	for _, statement := range statements {
		if _, err := old.ExecContext(ctx, statement); err != nil {
			t.Fatalf("Creating the old schema failed: %v", err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatalf("Closing the old database failed: %v", err)
	}
}

func testMigratedColumnKeys(t *testing.T, ctx context.Context, repo *Repo) {
	t.Helper()

	want := []domain.Column{{ID: "To Do", Name: "To Do", Position: 0}}
	columns, err := repo.GetColumns(ctx, defaultBoardID)
	if err != nil {
		t.Fatalf("GetColumns on a migrated database failed: %v", err)
	}
	if !reflect.DeepEqual(columns, want) {
		t.Fatalf("The existing column did not survive the migration: %+v", columns)
	}
	if got, err := repo.GetByID(ctx, defaultBoardID, "old_card"); err != nil || got.ColumnID != "To Do" {
		t.Fatalf("The existing card did not survive the migration: %+v, %v", got, err)
	}

	// The same card and column IDs now belong to each board separately.
	if _, err := repo.CreateBoard(ctx, domain.Board{ID: "bdb", Name: "Second"}); err != nil {
		t.Fatalf("CreateBoard failed: %v", err)
	}
	if _, err := repo.CreateColumn(ctx, "bdb", want[0]); err != nil {
		t.Fatalf("A second board cannot start with the same column: %v", err)
	}
	if _, err := repo.Create(ctx, "bdb", domain.Card{ID: "old_card", Title: "Second copy", ColumnID: "To Do", Priority: domain.PriorityLow}); !errors.Is(err, domain.ErrCardExists) {
		t.Errorf("Expected a card ID to stay globally unique, got %v", err)
	}

	// The duplicate is still refused inside one board.
	if _, err := repo.CreateColumn(ctx, defaultBoardID, want[0]); !errors.Is(err, domain.ErrColumnExists) {
		t.Errorf("Expected ErrColumnExists on a duplicate inside one board, got %v", err)
	}
}

func TestOpenRequiresAPath(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Error("Expected an error for an empty database path")
	}
}
