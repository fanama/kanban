package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	primaryHTTP "kanban-app/internal/adapters/primary/http"
	gsheetsRepo "kanban-app/internal/adapters/secondary/gsheets"
	memoryRepo "kanban-app/internal/adapters/secondary/memory"
	sqliteRepo "kanban-app/internal/adapters/secondary/sqlite"
	"kanban-app/internal/domain"
	"kanban-app/internal/ports"
	"kanban-app/internal/usecase"
)

const (
	defaultTemplateGlob = "internal/adapters/primary/http/templates/*.html"
	// defaultBoardID is the workspace a fresh storage starts with.
	defaultBoardID = "default"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func run() error {
	ctx := context.Background()

	// 1. Secondary adapter (driven): which storage backs the output port.
	repo, err := buildRepo(ctx)
	if err != nil {
		return err
	}
	if closer, ok := repo.(io.Closer); ok {
		defer closer.Close()
	}

	// 2. Application layer (core): use cases over the output port.
	boardUseCase := usecase.NewBoardUseCase(repo)

	// 3. Primary adapter (driving): HTTP + HTMX over the input port.
	templateGlob := envOr("TEMPLATE_GLOB", defaultTemplateGlob)
	handler, err := primaryHTTP.NewHTTPHandler(boardUseCase, templateGlob)
	if err != nil {
		return fmt.Errorf("template load failed: %w", err)
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	addr := ":" + envOr("PORT", "8080")
	fmt.Printf("Clean Architecture Kanban server running on %s...\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// buildRepo selects the storage adapter from the environment. Only this
// function knows the concrete adapters: the core only sees ports.BoardRepository.
func buildRepo(ctx context.Context) (ports.BoardRepository, error) {
	var (
		repo ports.BoardRepository
		err  error
	)

	switch kind := storageKind(); kind {
	case "sqlite":
		path := envOr("KANBAN_DB", "kanban.db")
		repo, err = sqliteRepo.Open(ctx, path)
		if err != nil {
			return nil, err
		}
		fmt.Printf("Using SQLite storage: %s\n", path)

	case "memory":
		fmt.Println("Using in-memory storage (nothing is persisted; set STORAGE=sqlite to keep the board).")
		repo = memoryRepo.NewMemoryRepo()

	case "sheets":
		cfg, err := sheetsConfig()
		if err != nil {
			return nil, err
		}
		sheetRepo, err := gsheetsRepo.NewSheetsRepo(ctx, cfg)
		if err != nil {
			return nil, err
		}
		fmt.Printf("Using Google Sheets storage: spreadsheet %s, tab %q\n", cfg.SpreadsheetID, sheetRepo.SheetName())
		repo = sheetRepo

	default:
		return nil, fmt.Errorf("unknown STORAGE %q (expected: sqlite, memory or sheets)", kind)
	}

	if err := seed(ctx, repo); err != nil {
		return nil, err
	}
	return repo, nil
}

// seed makes sure a fresh storage holds a board, its columns and a couple of
// demo cards. It only touches an empty board, so an existing one is never
// modified.
func seed(ctx context.Context, repo ports.BoardRepository) error {
	boards, err := repo.ListBoards(ctx)
	if err != nil {
		return err
	}

	var board domain.Board
	if len(boards) == 0 {
		board, err = repo.CreateBoard(ctx, domain.Board{ID: defaultBoardID, Name: "My board"})
		if err != nil {
			return err
		}
		fmt.Println("Created the default board " + board.Name + ".")
	} else {
		board = boards[0]
	}

	columns, err := repo.GetColumns(ctx, board.ID)
	if err != nil {
		return err
	}
	if len(columns) == 0 {
		for _, column := range domain.DefaultColumns() {
			if _, err := repo.CreateColumn(ctx, board.ID, column); err != nil {
				return err
			}
		}
		fmt.Println("Created the default columns: To Do, In Progress, Done.")
		columns = domain.DefaultColumns()
	}

	cards, err := repo.GetAll(ctx, board.ID)
	if err != nil {
		return err
	}
	if len(cards) == 0 {
		first := domain.SortColumns(columns)[0]
		for _, card := range demoCards(first.ID) {
			if _, err := repo.Create(ctx, board.ID, card); err != nil {
				return err
			}
		}
		fmt.Printf("Seeded the empty board with 2 demo cards in %q.\n", first.Name)
	}
	return nil
}

// storageKind resolves STORAGE, defaulting to sqlite. Giving a spreadsheet
// (SHEET_URL or SHEET_ID) still selects the Sheets adapter unless STORAGE says
// otherwise.
func storageKind() string {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE"))); v != "" {
		return v
	}
	if os.Getenv("SHEET_URL") != "" || os.Getenv("SHEET_ID") != "" {
		return "sheets"
	}
	return "sqlite"
}

// sheetsConfig reads the Sheets settings. SHEET_URL (paste the browser link,
// gid included) or SHEET_ID (bare ID) selects the tab.
func sheetsConfig() (gsheetsRepo.Config, error) {
	rawURL := os.Getenv("SHEET_URL")
	sheetID := os.Getenv("SHEET_ID")
	if rawURL == "" && sheetID == "" {
		return gsheetsRepo.Config{}, fmt.Errorf("STORAGE=sheets needs SHEET_URL or SHEET_ID")
	}

	var gid *int64
	if rawURL != "" {
		id, fromURL, err := gsheetsRepo.ParseSpreadsheetURL(rawURL)
		if err != nil {
			return gsheetsRepo.Config{}, err
		}
		sheetID, gid = id, fromURL
	}

	if raw := os.Getenv("SHEET_GID"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return gsheetsRepo.Config{}, fmt.Errorf("SHEET_GID must be a number: %w", err)
		}
		gid = &v
	}

	return gsheetsRepo.Config{
		SpreadsheetID:   sheetID,
		SheetName:       os.Getenv("SHEET_NAME"),
		SheetGID:        gid,
		CredentialsFile: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
	}, nil
}

func demoCards(columnID string) []domain.Card {
	return []domain.Card{
		{ID: "card_1", Title: "Clean Architecture Design", ColumnID: columnID, Priority: domain.PriorityHigh},
		{ID: "card_2", Title: "Wire HTMX Frontend", ColumnID: columnID, Priority: domain.PriorityMedium},
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
