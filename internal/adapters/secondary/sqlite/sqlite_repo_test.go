package sqlite

import (
	"context"
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
