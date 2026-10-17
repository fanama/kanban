// Package harness drives the application through its ports only: the use case
// (input port) for logic tests, and the HTTP adapter for end-to-end tests.
package harness

import (
	"context"
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

const (
	templateGlob = "../internal/adapters/primary/http/templates/*.html"
	staticDir    = "../internal/adapters/primary/http/static"
)

// board wraps the four services with one workspace ID, so the tests read like
// they did when the app held a single board.
type board struct {
	ports.BoardService
	ports.CardService
	ports.ColumnService
	ports.ImportExportService
	id string
}

func (b board) GetBoard(ctx context.Context) (domain.BoardView, error) {
	return b.BoardService.GetBoard(ctx, b.id)
}

func (b board) AddCard(ctx context.Context, title string, priority domain.Priority) (domain.BoardView, error) {
	return b.CardService.AddCard(ctx, b.id, "", title, priority)
}

func (b board) AddCardTo(ctx context.Context, columnID, title string, priority domain.Priority) (domain.BoardView, error) {
	return b.CardService.AddCard(ctx, b.id, columnID, title, priority)
}

func (b board) UpdateCard(ctx context.Context, card domain.Card) (domain.BoardView, error) {
	return b.CardService.UpdateCard(ctx, b.id, card)
}

func (b board) MoveCard(ctx context.Context, cardID, columnID string) (domain.BoardView, error) {
	return b.CardService.MoveCard(ctx, b.id, cardID, columnID)
}

func (b board) DeleteCard(ctx context.Context, cardID string) (domain.BoardView, error) {
	return b.CardService.DeleteCard(ctx, b.id, cardID)
}

func (b board) AddColumn(ctx context.Context, name string) (domain.BoardView, error) {
	return b.ColumnService.AddColumn(ctx, b.id, name)
}

func (b board) DeleteColumn(ctx context.Context, columnID string) (domain.BoardView, error) {
	return b.ColumnService.DeleteColumn(ctx, b.id, columnID)
}

func (b board) RenameColumn(ctx context.Context, columnID, name string) (domain.BoardView, error) {
	return b.ColumnService.RenameColumn(ctx, b.id, columnID, name)
}

func (b board) ExportBoard(ctx context.Context) (domain.BoardSnapshot, error) {
	return b.ImportExportService.ExportBoard(ctx, b.id)
}

func (b board) ImportBoard(ctx context.Context, snapshot domain.BoardSnapshot) (domain.BoardView, error) {
	return b.ImportExportService.ImportBoard(ctx, b.id, snapshot)
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
	services := usecase.NewServices(repo)
	return board{services.Board, services.Cards, services.Columns, services.Transfer, "TestBoard"}
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

	services := usecase.NewServices(repo)
	handler, err := primaryHTTP.NewHTTPHandler(services.Board, services.Cards, services.Columns, services.Transfer, templateGlob, staticDir)
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
