// Package harness drives the application through its ports only: the use case
// (input port) for logic tests, and the HTTP adapter for end-to-end tests.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	primaryHTTP "kanban-app/internal/adapters/primary/http"
	memoryRepo "kanban-app/internal/adapters/secondary/memory"
	"kanban-app/internal/domain"
	"kanban-app/internal/ports"
	"kanban-app/internal/usecase"
)

const templateGlob = "../internal/adapters/primary/http/templates/*.html"

// board wraps the use case with one workspace ID, so the tests read like they
// did when the app held a single board.
type board struct {
	ports.BoardUseCase
	id string
}

func (b board) GetBoard(ctx context.Context) (domain.BoardView, error) {
	return b.BoardUseCase.GetBoard(ctx, b.id)
}

func (b board) AddCard(ctx context.Context, title string, priority domain.Priority) (domain.BoardView, error) {
	return b.BoardUseCase.AddCard(ctx, b.id, title, priority)
}

func (b board) UpdateCard(ctx context.Context, card domain.Card) (domain.BoardView, error) {
	return b.BoardUseCase.UpdateCard(ctx, b.id, card)
}

func (b board) MoveCard(ctx context.Context, cardID, columnID string) (domain.BoardView, error) {
	return b.BoardUseCase.MoveCard(ctx, b.id, cardID, columnID)
}

func (b board) DeleteCard(ctx context.Context, cardID string) (domain.BoardView, error) {
	return b.BoardUseCase.DeleteCard(ctx, b.id, cardID)
}

func (b board) AddColumn(ctx context.Context, name string) (domain.BoardView, error) {
	return b.BoardUseCase.AddColumn(ctx, b.id, name)
}

func (b board) DeleteColumn(ctx context.Context, columnID string) (domain.BoardView, error) {
	return b.BoardUseCase.DeleteColumn(ctx, b.id, columnID)
}

func (b board) RenameColumn(ctx context.Context, columnID, name string) (domain.BoardView, error) {
	return b.BoardUseCase.RenameColumn(ctx, b.id, columnID, name)
}

func (b board) ExportBoard(ctx context.Context) (domain.BoardSnapshot, error) {
	return b.BoardUseCase.ExportBoard(ctx, b.id)
}

func (b board) ImportBoard(ctx context.Context, snapshot domain.BoardSnapshot) (domain.BoardView, error) {
	return b.BoardUseCase.ImportBoard(ctx, b.id, snapshot)
}

// newBoard returns a use case over a fresh in-memory board with the given
// column names.
func newBoard(t *testing.T, columnNames ...string) board {
	t.Helper()
	if len(columnNames) == 0 {
		columnNames = []string{"To Do", "Doing", "Done"}
	}

	columns := make([]domain.Column, 0, len(columnNames))
	for i, name := range columnNames {
		columns = append(columns, domain.Column{ID: name, Name: name, Position: i})
	}
	repo := memoryRepo.NewMemoryRepo(memoryRepo.MemoryBoard{
		ID: "TestBoard", Name: "Test", Columns: columns,
	})
	return board{usecase.NewBoardUseCase(repo), "TestBoard"}
}

func addCard(t *testing.T, uc board, title string) domain.Card {
	t.Helper()
	if _, err := uc.AddCard(context.Background(), title, domain.PriorityMedium); err != nil {
		t.Fatalf("AddCard(%q) failed: %v", title, err)
	}
	return findCard(t, uc, title)
}

func findCard(t *testing.T, uc board, title string) domain.Card {
	t.Helper()
	board, err := uc.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard failed: %v", err)
	}
	for _, c := range board.All() {
		if c.Title == title {
			return c
		}
	}
	t.Fatalf("Card %q not found", title)
	return domain.Card{}
}

func columnOf(t *testing.T, uc board, title string) string {
	t.Helper()
	board, err := uc.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard failed: %v", err)
	}
	position := board.Position(findCard(t, uc, title).ID)
	if position < 0 {
		t.Fatalf("Card %q is not on the board", title)
	}
	return board.Columns[position].Column.Name
}

func setDependencies(t *testing.T, uc board, card domain.Card, dependsOn ...string) {
	t.Helper()
	// Re-read the stored card: the caller may hold a stale copy, and overlaying
	// its old column would silently demote the card.
	board, err := uc.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard failed: %v", err)
	}
	stored, ok := board.Index()[card.ID]
	if !ok {
		t.Fatalf("Card %q not found while setting its constraints", card.Title)
	}
	stored.DependsOn = dependsOn
	if _, err := uc.UpdateCard(context.Background(), stored); err != nil {
		t.Fatalf("Setting the constraints of %q failed: %v", card.Title, err)
	}
}

func TestUseCasesOverDynamicColumns(t *testing.T) {
	ctx := context.Background()

	t.Run("AddCard puts the card in the first column", func(t *testing.T) {
		uc := newBoard(t)
		card := addCard(t, uc, "First")
		if card.ColumnID != "To Do" {
			t.Errorf("Expected the card in To Do, got %q", card.ColumnID)
		}
		if _, err := uc.AddCard(ctx, "   ", domain.PriorityLow); !errors.Is(err, domain.ErrEmptyTitle) {
			t.Errorf("Expected ErrEmptyTitle, got %v", err)
		}
	})

	t.Run("AddColumn appends a column and validates its name", func(t *testing.T) {
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
	})

	t.Run("MoveCard enforces the stage rule", func(t *testing.T) {
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
	})

	t.Run("a constraint pulls the cards that depend on it", func(t *testing.T) {
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
	})

	t.Run("edits pull dependents too, and a pull never breaks a promise", func(t *testing.T) {
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

		// A pull never breaks a promise: a pulled card whose own constraint has
		// not reached the required stage stays where it is.
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
	})

	t.Run("a backward move still demotes the dependents", func(t *testing.T) {
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
	})

	t.Run("a card with no constraint moves anywhere", func(t *testing.T) {
		uc := newBoard(t, "To Do", "Doing", "Done")
		card := addCard(t, uc, "Free")

		if _, err := uc.MoveCard(ctx, card.ID, "Done"); err != nil {
			t.Errorf("Expected a free card to reach the last column: %v", err)
		}
		if _, err := uc.MoveCard(ctx, card.ID, "To Do"); err != nil {
			t.Errorf("Expected a free card to go back: %v", err)
		}
	})

	t.Run("demoting a card drags its dependents down, transitively", func(t *testing.T) {
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
	})

	t.Run("DeleteColumn moves its cards to the previous column", func(t *testing.T) {
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
	})

	t.Run("RenameColumn replaces a column's name in place", func(t *testing.T) {
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
	})

	t.Run("deleting the first column hands its cards to the new first one", func(t *testing.T) {
		uc := newBoard(t, "To Do", "Doing", "Done")
		addCard(t, uc, "Started")

		if _, err := uc.DeleteColumn(ctx, "To Do"); err != nil {
			t.Fatalf("DeleteColumn failed: %v", err)
		}
		if got := columnOf(t, uc, "Started"); got != "Doing" {
			t.Errorf("Expected the card in the new first column, got %q", got)
		}
	})

	t.Run("deleting a column demotes the cards that depend on the moved ones", func(t *testing.T) {
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
	})

	t.Run("the board keeps at least one column", func(t *testing.T) {
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
	})

	t.Run("UpdateCard moves a card between columns and gates the move", func(t *testing.T) {
		uc := newBoard(t, "A", "B", "C")
		early := addCard(t, uc, "Early")
		late := addCard(t, uc, "Late")
		setDependencies(t, uc, late, early.ID)

		// A partial edit keeps the fields it does not send.
		edited := late
		edited.Title = "Late, renamed"
		edited.DependsOn = nil
		edited.ColumnID = "C"
		if _, err := uc.UpdateCard(ctx, edited); !errors.Is(err, domain.ErrCardBlocked) {
			t.Fatalf("Expected ErrCardBlocked for a jump to the last column, got %v", err)
		}

		edited.ColumnID = "B"
		board, err := uc.UpdateCard(ctx, edited)
		if err != nil {
			t.Fatalf("UpdateCard failed: %v", err)
		}
		for _, c := range board.All() {
			if c.ID == late.ID {
				if len(c.DependsOn) != 1 || c.DependsOn[0] != early.ID {
					t.Errorf("A nil constraint list must keep the stored constraints, got %v", c.DependsOn)
				}
				if c.ColumnID != "B" || c.Title != "Late, renamed" {
					t.Errorf("Unexpected card after the edit: %+v", c)
				}
			}
		}

		if _, err := uc.UpdateCard(ctx, domain.Card{ID: late.ID, Title: "Ghost", ColumnID: "nope"}); !errors.Is(err, domain.ErrColumnNotFound) {
			t.Errorf("Expected ErrColumnNotFound, got %v", err)
		}
	})

	t.Run("constraints are validated", func(t *testing.T) {
		uc := newBoard(t, "A", "B")
		card := addCard(t, uc, "Card")

		ghost := card
		ghost.DependsOn = []string{"does-not-exist"}
		if _, err := uc.UpdateCard(ctx, ghost); !errors.Is(err, domain.ErrUnknownDependency) {
			t.Errorf("Expected ErrUnknownDependency, got %v", err)
		}

		self := card
		self.DependsOn = []string{card.ID}
		if _, err := uc.UpdateCard(ctx, self); !errors.Is(err, domain.ErrSelfDependency) {
			t.Errorf("Expected ErrSelfDependency, got %v", err)
		}

		other := addCard(t, uc, "Other")
		setDependencies(t, uc, other, card.ID)
		cyclic := card
		cyclic.DependsOn = []string{other.ID}
		if _, err := uc.UpdateCard(ctx, cyclic); !errors.Is(err, domain.ErrDependencyCycle) {
			t.Errorf("Expected ErrDependencyCycle, got %v", err)
		}
	})
}

func newHarness(t *testing.T) (*http.ServeMux, *httptest.Server) {
	t.Helper()

	seed := []domain.Card{
		{ID: "card_100", Title: "Ports & Adapters Unit Test", ColumnID: "To Do", Priority: domain.PriorityHigh},
		{ID: "card_200", Title: "Foundation", ColumnID: "Doing", Priority: domain.PriorityMedium},
	}
	repo := memoryRepo.NewMemoryRepo(memoryRepo.MemoryBoard{
		ID: "TestBoard", Name: "Test",
		Columns: []domain.Column{
			{ID: "To Do", Name: "To Do", Position: 0},
			{ID: "Doing", Name: "Doing", Position: 1},
			{ID: "Done", Name: "Done", Position: 2},
		},
		Cards: seed,
	})

	handler, err := primaryHTTP.NewHTTPHandler(usecase.NewBoardUseCase(repo), templateGlob)
	if err != nil {
		t.Fatalf("Handler init failed: %v", err)
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return mux, ts
}

// get performs an HTMX GET and returns the status and the body.
func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("Building the request failed: %v", err)
	}
	req.Header.Set("HX-Request", "true")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP call failed: %v", err)
	}
	defer res.Body.Close()
	return res.StatusCode, readBody(t, res)
}

// post performs an HTMX POST and returns the status and the body.
func post(t *testing.T, url string, form url.Values) (int, string) {
	t.Helper()
	req, err := http.NewRequest("POST", url, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("Building the request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP call failed: %v", err)
	}
	defer res.Body.Close()
	return res.StatusCode, readBody(t, res)
}

func TestHTTPAdapter(t *testing.T) {
	_, ts := newHarness(t)

	t.Run("GET / renders every column", func(t *testing.T) {
		status, body := get(t, ts.URL+"/")
		if status != http.StatusOK {
			t.Fatalf("Expected 200, got %d", status)
		}
		for _, want := range []string{">To Do<", ">Doing<", ">Done<", "Ports &amp; Adapters Unit Test", "Foundation"} {
			if !strings.Contains(body, want) {
				t.Errorf("Board missing %q", want)
			}
		}
		if !strings.Contains(body, `id="board"`) {
			t.Error("Expected the board fragment in an HTMX response")
		}
	})

	t.Run("POST /cards adds a card to the first column", func(t *testing.T) {
		status, body := post(t, ts.URL+"/cards", url.Values{"title": {"Added via HTTP"}, "priority": {"High"}})
		if status != http.StatusOK {
			t.Fatalf("Expected 200, got %d", status)
		}
		if !strings.Contains(body, "Added via HTTP") {
			t.Errorf("Expected the new card on the board: %s", body)
		}
	})

	t.Run("POST /cards/move advances, blocks and demotes", func(t *testing.T) {
		// card_100 goes one stage forward.
		if status, _ := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_100"}, "column_id": {"Doing"}}); status != http.StatusOK {
			t.Fatalf("Expected 200, got %d", status)
		}

		// card_200 is the constraint of card_100, both sit in Doing.
		if status, _ := post(t, ts.URL+"/cards/card_100", url.Values{
			"title": {"Ports & Adapters Unit Test"}, "column_id": {"Doing"}, "depends_on": {"card_200"},
		}); status != http.StatusOK {
			t.Fatalf("Expected 200 while saving the constraint, got %d", status)
		}

		// card_200 steps back: card_100 is one stage behind, so it stays put.
		status, body := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_200"}, "column_id": {"To Do"}})
		if status != http.StatusOK {
			t.Fatalf("Expected 200, got %d", status)
		}
		if !strings.Contains(body, "Ports &amp; Adapters Unit Test") {
			t.Errorf("Expected card_100 to keep its column: %s", body)
		}

		// Reaching Done needs its constraint at Doing or further: it is at To Do.
		status, body = post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_100"}, "column_id": {"Done"}})
		if status != http.StatusBadRequest {
			t.Fatalf("Expected 400 for a blocked card, got %d", status)
		}
		if !strings.Contains(body, "Foundation") {
			t.Errorf("Expected the blocking card in %q", body)
		}

		if status, _ := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_200"}, "column_id": {"nope"}}); status != http.StatusNotFound {
			t.Errorf("Expected 404 for an unknown column, got %d", status)
		}

		// Moving the constraint one stage forward releases card_100.
		if status, _ := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_200"}, "column_id": {"Doing"}}); status != http.StatusOK {
			t.Fatalf("Expected 200, got %d", status)
		}
		if status, _ := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_100"}, "column_id": {"Done"}}); status != http.StatusOK {
			t.Errorf("Expected the card to advance, got %d", status)
		}
	})

	t.Run("moving a constraint pulls its dependents along", func(t *testing.T) {
		// Both cards in the first column, card_100 waiting for card_200.
		if status, _ := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_100"}, "column_id": {"To Do"}}); status != http.StatusOK {
			t.Fatalf("Expected 200, got %d", status)
		}
		if status, _ := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_200"}, "column_id": {"To Do"}}); status != http.StatusOK {
			t.Fatalf("Expected 200, got %d", status)
		}
		if status, _ := post(t, ts.URL+"/cards/card_100", url.Values{
			"title": {"Ports & Adapters Unit Test"}, "column_id": {"To Do"}, "depends_on": {"card_200"},
		}); status != http.StatusOK {
			t.Fatalf("Expected 200 while saving the constraint, got %d", status)
		}

		// Advancing the constraint carries the dependent with it.
		status, body := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_200"}, "column_id": {"Doing"}})
		if status != http.StatusOK {
			t.Fatalf("Expected 200, got %d (%s)", status, body)
		}
		if !strings.Contains(body, "Ports &amp; Adapters Unit Test") {
			t.Errorf("Expected card_100 in the returned board: %s", body)
		}

		// The board keeps showing who awaits whom, without a locked button:
		// nobody is stuck, the pull handles it.
		status, body = get(t, ts.URL+"/")
		if status != http.StatusOK {
			t.Fatalf("Expected 200, got %d", status)
		}
		if strings.Contains(body, `title="The cards that depend on this one must move first"`) {
			t.Errorf("Expected no locked button, the pull removes the deadlock: %s", body)
		}
	})

	t.Run("GET /cards/{id}/edit offers the columns and the constraints", func(t *testing.T) {
		status, body := get(t, ts.URL+"/cards/card_200/edit")
		if status != http.StatusOK {
			t.Fatalf("Expected 200, got %d", status)
		}
		for _, want := range []string{`name="column_id"`, `name="depends_on"`, ">To Do<", ">Doing<", ">Done<", "Ports &amp; Adapters Unit Test"} {
			if !strings.Contains(body, want) {
				t.Errorf("Edit form missing %q: %s", want, body)
			}
		}
	})

	t.Run("POST /cards/{id} saves the edit", func(t *testing.T) {
		status, body := post(t, ts.URL+"/cards/card_100", url.Values{
			"title":       {"Edited from the browser"},
			"description": {"A real description"},
			"objective":   {"Ship it"},
			"column_id":   {"Doing"},
			"priority":    {"Low"},
			"depends_on":  {"card_200"},
		})
		if status != http.StatusOK {
			t.Fatalf("Expected 200, got %d: %s", status, body)
		}
		for _, want := range []string{"Edited from the browser", "A real description", "Ship it"} {
			if !strings.Contains(body, want) {
				t.Errorf("Board after the edit is missing %q: %s", want, body)
			}
		}

		if status, _ := post(t, ts.URL+"/cards/card_100", url.Values{"title": {" "}, "column_id": {"Doing"}}); status != http.StatusBadRequest {
			t.Errorf("Expected 400 for an empty title, got %d", status)
		}
		if status, _ := post(t, ts.URL+"/cards/ghost", url.Values{"title": {"Ghost"}, "column_id": {"Doing"}}); status != http.StatusNotFound {
			t.Errorf("Expected 404 for an unknown card, got %d", status)
		}
	})

	t.Run("POST /columns adds and POST /columns/{id}/delete removes", func(t *testing.T) {
		status, body := post(t, ts.URL+"/columns", url.Values{"name": {"Review"}})
		if status != http.StatusOK {
			t.Fatalf("Expected 200, got %d", status)
		}
		if !strings.Contains(body, ">Review</span>") {
			t.Fatalf("Expected the new column on the board: %s", body)
		}

		// The column was appended last: its delete form is the last one rendered.
		actions := columnDeletePattern.FindAllStringSubmatch(body, -1)
		if len(actions) != 4 {
			t.Fatalf("Expected 4 deletable columns, got %d", len(actions))
		}
		added := actions[len(actions)-1][1]

		status, body = post(t, ts.URL+"/columns/"+added+"/delete", nil)
		if status != http.StatusOK {
			t.Fatalf("Expected 200, got %d (%s)", status, body)
		}
		if strings.Contains(body, ">Review</span>") {
			t.Errorf("Expected the column to be gone: %s", body)
		}

		if status, _ := post(t, ts.URL+"/columns", url.Values{"name": {"  "}}); status != http.StatusBadRequest {
			t.Errorf("Expected 400 for an empty column name, got %d", status)
		}
		if status, _ := post(t, ts.URL+"/columns/nope/delete", nil); status != http.StatusNotFound {
			t.Errorf("Expected 404 for an unknown column, got %d", status)
		}
	})

	t.Run("GET /columns/{id}/rename opens the form and POST replaces the name", func(t *testing.T) {
		status, body := get(t, ts.URL+"/columns/Doing/rename")
		if status != http.StatusOK {
			t.Fatalf("Expected 200, got %d (%s)", status, body)
		}
		if !strings.Contains(body, `name="name"`) || !strings.Contains(body, `value="Doing"`) {
			t.Errorf("Expected the rename form prefilled with the current name: %s", body)
		}

		status, body = post(t, ts.URL+"/columns/Doing/rename", url.Values{"name": {"Work in progress"}})
		if status != http.StatusOK {
			t.Fatalf("Expected 200, got %d (%s)", status, body)
		}
		if !strings.Contains(body, ">Work in progress</span>") {
			t.Errorf("Expected the renamed column on the board: %s", body)
		}
		if strings.Contains(body, ">Doing</span>") {
			t.Errorf("Expected the old name to be gone: %s", body)
		}

		// The cancel path: GET /columns/{id} answers with the quiet header.
		status, body = get(t, ts.URL+"/columns/Doing")
		if status != http.StatusOK || strings.Contains(body, `name="name"`) {
			t.Errorf("Expected the plain header after cancel, got %d: %s", status, body)
		}
	
		if status, _ := post(t, ts.URL+"/columns/Doing/rename", url.Values{"name": {"to do"}}); status != http.StatusBadRequest {
			t.Errorf("Expected 400 for a case-insensitive duplicate name, got %d", status)
		}
		if status, _ := post(t, ts.URL+"/columns/nope/rename", url.Values{"name": {"X"}}); status != http.StatusNotFound {
			t.Errorf("Expected 404 for an unknown column, got %d", status)
		}
	})

	t.Run("GET /cards/{id} returns the read-only card", func(t *testing.T) {
		status, body := get(t, ts.URL+"/cards/card_100")
		if status != http.StatusOK {
			t.Fatalf("Expected 200, got %d", status)
		}
		if strings.Contains(body, `name="title"`) {
			t.Errorf("Expected the read-only card, got a form: %s", body)
		}
	})
}

// columnDeletePattern captures the ID of a column from its delete form.
var columnDeletePattern = regexp.MustCompile(`action="/columns/([^"/]+)/delete"`)

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("Reading body failed: %v", err)
	}
	return string(body)
}

// --- JSON export / import ---------------------------------------------------

func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()

	t.Run("ExportBoard snapshots the columns in order and every card", func(t *testing.T) {
		uc := newBoard(t, "To Do", "Doing", "Done")
		early := addCard(t, uc, "Early")
		late := addCard(t, uc, "Late")
		setDependencies(t, uc, late, early.ID)
		if _, err := uc.MoveCard(ctx, early.ID, "Doing"); err != nil {
			t.Fatalf("MoveCard failed: %v", err)
		}

		snapshot, err := uc.ExportBoard(ctx)
		if err != nil {
			t.Fatalf("ExportBoard failed: %v", err)
		}
		if len(snapshot.Columns) != 3 || snapshot.Columns[0].Name != "To Do" {
			t.Errorf("Expected the columns in order, got %+v", snapshot.Columns)
		}
		if len(snapshot.Cards) != 2 {
			t.Errorf("Expected 2 cards, got %+v", snapshot.Cards)
		}
		for _, c := range snapshot.Cards {
			if c.ID == late.ID && (len(c.DependsOn) != 1 || c.DependsOn[0] != early.ID) {
				t.Errorf("Expected the constraint in the snapshot, got %+v", c)
			}
		}
	})

	t.Run("ImportBoard rebuilds the board from a snapshot", func(t *testing.T) {
		uc := newBoard(t, "To Do", "Doing", "Done")
		eraseMe := addCard(t, uc, "Erase me")
		early := addCard(t, uc, "Early")
		setDependencies(t, uc, early, eraseMe.ID) // a dependency across the board

		snapshot, err := uc.ExportBoard(ctx)
		if err != nil {
			t.Fatalf("ExportBoard failed: %v", err)
		}

		// Load it into another board — as another person would, maybe backed
		// by a different storage adapter.
		fresh := usecase.NewBoardUseCase(memoryRepo.NewMemoryRepo())
		workspace, err := fresh.CreateBoard(ctx, "Fresh")
		if err != nil {
			t.Fatalf("CreateBoard failed: %v", err)
		}
		board, err := fresh.ImportBoard(ctx, workspace.ID, snapshot)
		if err != nil {
			t.Fatalf("ImportBoard failed: %v", err)
		}
		if len(board.Columns) != 3 {
			t.Errorf("Expected the 3 columns to come back, got %+v", board.OrderedColumns())
		}
		if _, ok := board.Index()[eraseMe.ID]; !ok {
			t.Error("Expected the imported cards, not the previous content")
		}
		stale, ok := board.Index()[early.ID]
		if !ok {
			t.Fatal("Expected Early to be imported")
		}
		if len(stale.DependsOn) != 1 || stale.DependsOn[0] != eraseMe.ID {
			t.Errorf("Expected the constraints to survive the trip, got %v", stale.DependsOn)
		}
		if got := board.Position(early.ID); got != 0 {
			t.Errorf("Expected Early back in the first column (position 0), got %d", got)
		}

		// The same file imports cleanly again: a snapshot does not decay.
		if _, err := fresh.ImportBoard(ctx, workspace.ID, snapshot); err != nil {
			t.Errorf("Re-importing the same snapshot failed: %v", err)
		}
	})

	t.Run("ImportBoard applies the board rules to the file", func(t *testing.T) {
		uc := newBoard(t)

		tests := []struct {
			name  string
			snap  domain.BoardSnapshot
			isErr error
		}{
			{
				name:  "a file with no column",
				snap:  domain.BoardSnapshot{},
				isErr: domain.ErrInvalidImport,
			},
			{
				name: "an empty title",
				snap: domain.BoardSnapshot{
					Columns: []domain.Column{{ID: "A", Name: "A", Position: 0}},
					Cards:   []domain.Card{{ID: "c1", Title: "  ", ColumnID: "A"}},
				},
				isErr: domain.ErrEmptyTitle,
			},
			{
				name: "a card without an ID",
				snap: domain.BoardSnapshot{
					Columns: []domain.Column{{ID: "A", Name: "A", Position: 0}},
					Cards:   []domain.Card{{Title: "Ghost", ColumnID: "A"}},
				},
				isErr: domain.ErrInvalidImport,
			},
			{
				name: "a duplicate card ID",
				snap: domain.BoardSnapshot{
					Columns: []domain.Column{{ID: "A", Name: "A", Position: 0}},
					Cards: []domain.Card{
						{ID: "c1", Title: "First", ColumnID: "A"},
						{ID: "c1", Title: "Second", ColumnID: "A"},
					},
				},
				isErr: domain.ErrInvalidImport,
			},
			{
				name: "a duplicated column ID",
				snap: domain.BoardSnapshot{
					Columns: []domain.Column{
						{ID: "A", Name: "A", Position: 0},
						{ID: "A", Name: "B", Position: 1},
					},
				},
				isErr: domain.ErrInvalidImport,
			},
			{
				name: "an empty column name",
				snap: domain.BoardSnapshot{
					Columns: []domain.Column{
						{ID: "A", Name: "  ", Position: 0},
					},
				},
				isErr: domain.ErrEmptyColumnName,
			},
			{
				name: "an unknown constraint",
				snap: domain.BoardSnapshot{
					Columns: []domain.Column{{ID: "A", Name: "A", Position: 0}},
					Cards:   []domain.Card{{ID: "c1", Title: "Alone", ColumnID: "A", DependsOn: []string{"ghost"}}},
				},
				isErr: domain.ErrUnknownDependency,
			},
			{
				name: "a self constraint",
				snap: domain.BoardSnapshot{
					Columns: []domain.Column{{ID: "A", Name: "A", Position: 0}},
					Cards:   []domain.Card{{ID: "c1", Title: "Snake", ColumnID: "A", DependsOn: []string{"c1"}}},
				},
				isErr: domain.ErrSelfDependency,
			},
			{
				name: "a constraint cycle",
				snap: domain.BoardSnapshot{
					Columns: []domain.Column{{ID: "A", Name: "A", Position: 0}},
					Cards: []domain.Card{
						{ID: "c1", Title: "One", ColumnID: "A", DependsOn: []string{"c2"}},
						{ID: "c2", Title: "Two", ColumnID: "A", DependsOn: []string{"c1"}},
					},
				},
				isErr: domain.ErrDependencyCycle,
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				_, err := uc.ImportBoard(ctx, tc.snap)
				if err == nil {
					t.Fatalf("Expected %v, got no error", tc.isErr)
				}
				if !errors.Is(err, tc.isErr) {
					t.Fatalf("Expected %v, got %v", tc.isErr, err)
				}
			})
		}
	})

	t.Run("a card whose column is unknown falls back to the first column", func(t *testing.T) {
		uc := newBoard(t)
		board, err := uc.ImportBoard(ctx, domain.BoardSnapshot{
			Columns: []domain.Column{
				{ID: "A", Name: "A", Position: 0},
				{ID: "B", Name: "B", Position: 1},
			},
			Cards: []domain.Card{{ID: "c1", Title: "Stray", ColumnID: "ghosted"}},
		})
		if err != nil {
			t.Fatalf("ImportBoard failed: %v", err)
		}
		if got, ok := board.Index()["c1"]; !ok || got.ColumnID != "A" {
			t.Errorf("Expected the stray card in the first column, got %+v", got)
		}
	})

	t.Run("deleting a card keeps the dependents and their other constraints", func(t *testing.T) {
		uc := newBoard(t)
		victim := addCard(t, uc, "Victim")
		keeper := addCard(t, uc, "Keeper")
		holder := addCard(t, uc, "Holder")
		setDependencies(t, uc, holder, victim.ID, keeper.ID)

		if _, err := uc.DeleteCard(ctx, victim.ID); err != nil {
			t.Fatalf("DeleteCard failed: %v", err)
		}

		board, err := uc.GetBoard(ctx)
		if err != nil {
			t.Fatalf("GetBoard failed: %v", err)
		}
		if _, ok := board.Index()[victim.ID]; ok {
			t.Error("Expected the deleted card to be gone")
		}
		if _, ok := board.Index()[keeper.ID]; !ok {
			t.Error("Expected the other cards to stay")
		}
		after, ok := board.Index()[holder.ID]
		if !ok {
			t.Fatal("Expected the dependent to stay")
		}
		if len(after.DependsOn) != 1 || after.DependsOn[0] != keeper.ID {
			t.Errorf("Expected the constraint dropped and the other kept, got %v", after.DependsOn)
		}

		if _, err := uc.DeleteCard(ctx, "ghost"); !errors.Is(err, domain.ErrCardNotFound) {
			t.Errorf("Expected ErrCardNotFound for a deleted card, got %v", err)
		}
	})

	t.Run("boards are created, named and listed", func(t *testing.T) {
		uc := newBoard(t)

		created, err := uc.CreateBoard(ctx, "Side quest")
		if err != nil {
			t.Fatalf("CreateBoard failed: %v", err)
		}
		if created.ID == "" || created.Name != "Side quest" {
			t.Errorf("Unexpected board: %+v", created)
		}

		boards, err := uc.ListBoards(ctx)
		if err != nil {
			t.Fatalf("ListBoards failed: %v", err)
		}
		if len(boards) != 2 {
			t.Fatalf("Expected 2 boards, got %+v", boards)
		}

		if _, err := uc.CreateBoard(ctx, "   "); !errors.Is(err, domain.ErrEmptyBoardName) {
			t.Errorf("Expected ErrEmptyBoardName, got %v", err)
		}
		if _, err := uc.CreateBoard(ctx, "side QUEST"); !errors.Is(err, domain.ErrBoardExists) {
			t.Errorf("Expected ErrBoardExists for a duplicated name, got %v", err)
		}
	})

	t.Run("boards do not leak content into each other", func(t *testing.T) {
		uc := newBoard(t)
		cut, err := uc.CreateBoard(ctx, "Cut")
		if err != nil {
			t.Fatalf("CreateBoard failed: %v", err)
		}

		// The test board gets a card through the wrapper (its own workspace).
		if _, err := uc.AddCard(ctx, "Only on the test board", domain.PriorityLow); err != nil {
			t.Fatalf("AddCard failed: %v", err)
		}

		fresh, err := uc.BoardUseCase.GetBoard(ctx, cut.ID)
		if err != nil {
			t.Fatalf("GetBoard failed: %v", err)
		}
		if fresh.Count() != 0 {
			t.Errorf("Expected the fresh board empty, got %+v", fresh.All())
		}

		// The fresh board gets its own card: the test board's card stays there.
		if _, err := uc.BoardUseCase.AddCard(ctx, cut.ID, "Cut card", domain.PriorityLow); err != nil {
			t.Fatalf("AddCard on the fresh board failed: %v", err)
		}
		if got := columnOf(t, uc, "Only on the test board"); got != "To Do" {
			t.Errorf("Expected the other board's card untouched, got %q", got)
		}
	})

	t.Run("a deleted board takes its columns and cards, and the last one stays", func(t *testing.T) {
		uc := newBoard(t)
		added, err := uc.CreateBoard(ctx, "Doomed")
		if err != nil {
			t.Fatalf("CreateBoard failed: %v", err)
		}
		if _, err := uc.BoardUseCase.AddColumn(ctx, added.ID, "Only column"); err != nil {
			t.Fatalf("AddColumn failed: %v", err)
		}
		if _, err := uc.BoardUseCase.AddCard(ctx, added.ID, "Doom card", domain.PriorityLow); err != nil {
			t.Fatalf("AddCard failed: %v", err)
		}

		if err := uc.DeleteBoard(ctx, added.ID); err != nil {
			t.Fatalf("DeleteBoard failed: %v", err)
		}
		if _, err := uc.BoardUseCase.GetBoard(ctx, added.ID); !errors.Is(err, domain.ErrBoardNotFound) {
			t.Errorf("Expected ErrBoardNotFound after deletion, got %v", err)
		}

		boards, err := uc.ListBoards(ctx)
		if err != nil {
			t.Fatalf("ListBoards failed: %v", err)
		}
		if len(boards) != 1 {
			t.Fatalf("Expected 1 board left, got %+v", boards)
		}
		if err := uc.DeleteBoard(ctx, boards[0].ID); !errors.Is(err, domain.ErrLastBoard) {
			t.Errorf("Expected ErrLastBoard for the last board, got %v", err)
		}
		if err := uc.DeleteBoard(ctx, "ghost"); !errors.Is(err, domain.ErrLastBoard) {
			t.Errorf("Expected ErrLastBoard even with nowhere to fall back, got %v", err)
		}

		// The surviving board still works.
		if _, err := uc.AddCard(ctx, "Still alive", domain.PriorityLow); err != nil {
			t.Errorf("AddCard on the surviving board failed: %v", err)
		}
	})
}

func TestExportImportHTTP(t *testing.T) {
	_, ts := newHarness(t)

	t.Run("GET /export.json downloads the board as JSON", func(t *testing.T) {
		res, err := http.Get(ts.URL + "/export.json")
		if err != nil {
			t.Fatalf("HTTP call failed: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200, got %d", res.StatusCode)
		}
		if !strings.Contains(res.Header.Get("Content-Type"), "application/json") {
			t.Errorf("Expected a JSON content type, got %q", res.Header.Get("Content-Type"))
		}
		if !strings.Contains(res.Header.Get("Content-Disposition"), "attachment") {
			t.Errorf("Expected an attachment disposition, got %q", res.Header.Get("Content-Disposition"))
		}

		snapshot, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("Reading body failed: %v", err)
		}
		var board domain.BoardSnapshot
		if err := json.Unmarshal(snapshot, &board); err != nil {
			t.Fatalf("The file is not a board snapshot: %v", err)
		}
		if len(board.Columns) != 3 || len(board.Cards) != 2 {
			t.Errorf("Expected 3 columns and 2 cards, got %+v", board)
		}
	})

	t.Run("POST /import.json replaces the board", func(t *testing.T) {
		payload := `{"columns":[
			  {"id":"Backlog","name":"Backlog","position":0},
			  {"id":"Shipped","name":"Shipped","position":1}],
			 "cards":[
			  {"id":"imp_1","title":"Imported via HTTP","column_id":"Shipped","priority":"High","depends_on":["imp_2"]},
			  {"id":"imp_2","title":"Seed","column_id":"Backlog"}]}`
		req, err := http.NewRequest("POST", ts.URL+"/import.json", strings.NewReader(payload))
		if err != nil {
			t.Fatalf("Building the request failed: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("HX-Request", "true")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("HTTP call failed: %v", err)
		}
		defer res.Body.Close()
		status, body := res.StatusCode, readBody(t, res)
		if status != http.StatusOK {
			t.Fatalf("Expected 200, got %d (%s)", status, body)
		}
		for _, want := range []string{"Imported via HTTP", ">Backlog<", ">Shipped<"} {
			if !strings.Contains(body, want) {
				t.Errorf("Expected the imported board in the response, missing %q: %s", want, body)
			}
		}
		if strings.Contains(body, "Ports &amp; Adapters Unit Test") {
			t.Errorf("Expected the previous cards to be gone: %s", body)
		}
	})

	t.Run("POST /import.json refuses a file that is not a board", func(t *testing.T) {
		req, err := http.NewRequest("POST", ts.URL+"/import.json", strings.NewReader("not json at all"))
		if err != nil {
			t.Fatalf("Building the request failed: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("HTTP call failed: %v", err)
		}
		defer res.Body.Close()
		body := readBody(t, res)
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d (%s)", res.StatusCode, body)
		}
	})

	t.Run("POST /import.json without a file is explained", func(t *testing.T) {
		res, err := http.Post(ts.URL+"/import.json", "text/plain", strings.NewReader(""))
		if err != nil {
			t.Fatalf("HTTP call failed: %v", err)
		}
		defer res.Body.Close()
		body := readBody(t, res)
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d (%s)", res.StatusCode, body)
		}
	})
}

// TestBoardsHTTP covers the boards bar: create, switch, isolation and delete.
func TestBoardsHTTP(t *testing.T) {
	_, ts := newHarness(t)

	// make performs an HTMX POST and returns the status, the body and the
	// cookies the server set (the test client keeps none).
	make := func(url string, form url.Values) (int, string, []*http.Cookie) {
		t.Helper()
		req, err := http.NewRequest("POST", url, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatalf("Building the request failed: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("HTTP call failed: %v", err)
		}
		defer res.Body.Close()
		return res.StatusCode, readBody(t, res), res.Cookies()
	}

	t.Run("POST /boards creates a board with default columns and switches to it", func(t *testing.T) {
		status, body, cookies := make(ts.URL+"/boards", url.Values{"name": {"Side quest"}})
		if status != http.StatusOK {
			t.Fatalf("Expected 200, got %d (%s)", status, body)
		}
		for _, want := range []string{"Side quest", ">To Do<", ">In Progress<", ">Done<"} {
			if !strings.Contains(body, want) {
				t.Errorf("Expected the fresh board bar/columns in the response, missing %q: %s", want, body)
			}
		}
		if !strings.Contains(body, "No cards") {
			t.Errorf("Expected the fresh board to be empty, got: %s", body)
		}

		// The browser follows the server's choice: the selection cookie holds
		// the new board.
		sideQuest := ""
		for _, cookie := range cookies {
			if cookie.Name == "kanban_board" {
				sideQuest = cookie.Value
			}
		}
		if sideQuest == "" {
			t.Fatal("Expected the selection cookie to be set")
		}

		// The seeded board keeps its cards on its own page.
		status, body = get(t, ts.URL+"/?board=TestBoard")
		if status != http.StatusOK || !strings.Contains(body, "Ports &amp; Adapters Unit Test") {
			t.Errorf("Expected the seeded board intact, got %d (%s)", status, body)
		}

		// Isolation: the other board has none of them.
		status, body = get(t, ts.URL+"/?board="+sideQuest)
		if status != http.StatusOK || strings.Contains(body, "Ports &amp; Adapters Unit Test") {
			t.Errorf("Expected the other board empty, got %d (%s)", status, body)
		}

		// Deleting it lands the browser back on the seeded board.
		status, body, _ = make(ts.URL+"/boards/"+sideQuest+"/delete", nil)
		if status != http.StatusOK || !strings.Contains(body, "Ports &amp; Adapters Unit Test") {
			t.Errorf("Expected to land on the surviving board, got %d (%s)", status, body)
		}

		// The last board refuses to go.
		status, body, _ = make(ts.URL+"/boards/TestBoard/delete", nil)
		if status != http.StatusBadRequest || !strings.Contains(body, "at least one board") {
			t.Errorf("Expected a refusal for the last board, got %d (%s)", status, body)
		}
	})
}

// TestDeleteCardHTTP covers the browser's delete button: the card goes, its
// dependents lose only this constraint, and a second delete is a 404.
func TestDeleteCardHTTP(t *testing.T) {
	_, ts := newHarness(t)

	if status, _ := post(t, ts.URL+"/cards/card_100", url.Values{
		"title": {"Ports & Adapters Unit Test"}, "column_id": {"To Do"}, "depends_on": {"card_200"},
	}); status != http.StatusOK {
		t.Fatalf("Expected 200 while saving the constraint, got %d", status)
	}

	status, body := post(t, ts.URL+"/cards/card_200/delete", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d (%s)", status, body)
	}
	if strings.Contains(body, "Foundation") {
		t.Errorf("Expected the deleted card to be gone from the board: %s", body)
	}
	if !strings.Contains(body, "Ports &amp; Adapters Unit Test") {
		t.Errorf("Expected the other cards to stay: %s", body)
	}

	// The edit form offers no ghost checkbox: the constraint is really dropped.
	status, body = get(t, ts.URL+"/cards/card_100/edit")
	if status != http.StatusOK || strings.Contains(body, `value="card_200" checked`) {
		t.Errorf("Expected the constraint to be dropped from the edit form, got %d (%s)", status, body)
	}

	if status, _ := post(t, ts.URL+"/cards/card_200/delete", nil); status != http.StatusNotFound {
		t.Errorf("Expected 404 for a deleted card, got %d", status)
	}
}
