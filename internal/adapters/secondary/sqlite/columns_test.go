package sqlite

import (
	"context"
	"errors"
	"kanban-app/internal/domain"
	"path/filepath"
	"reflect"
	"testing"
)

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

	testSQLiteColumnsRenumber(t, ctx, repo)
}

// testSQLiteColumnsRenumber checks that deleting a column closes the gap: the
// next column created must land after the survivors, never on a duplicate
// position.
func testSQLiteColumnsRenumber(t *testing.T, ctx context.Context, repo *Repo) {
	t.Helper()

	if err := repo.DeleteColumn(ctx, testBoard, "col_b"); err != nil {
		t.Fatalf("DeleteColumn failed: %v", err)
	}
	columns, err := repo.GetColumns(ctx, testBoard)
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
