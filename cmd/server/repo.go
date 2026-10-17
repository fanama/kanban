package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	gsheetsRepo "kanban-app/internal/adapters/secondary/gsheets"
	memoryRepo "kanban-app/internal/adapters/secondary/memory"
	sqliteRepo "kanban-app/internal/adapters/secondary/sqlite"
	"kanban-app/internal/ports"
)

// buildRepo selects the storage adapter from the environment, then makes sure
// it holds a usable board.
func buildRepo(ctx context.Context) (ports.BoardRepository, error) {
	repo, err := openRepo(ctx, storageKind())
	if err != nil {
		return nil, err
	}
	if err := seed(ctx, repo); err != nil {
		return nil, err
	}
	return repo, nil
}

// openRepo is the only place that names a concrete adapter: the core only ever
// sees ports.BoardRepository.
func openRepo(ctx context.Context, kind string) (ports.BoardRepository, error) {
	switch kind {
	case "sqlite":
		path := envOr("KANBAN_DB", "kanban.db")
		repo, err := sqliteRepo.Open(ctx, path)
		if err != nil {
			return nil, err
		}
		fmt.Printf("Using SQLite storage: %s\n", path)
		return repo, nil

	case "memory":
		fmt.Println("Using in-memory storage (nothing is persisted; set STORAGE=sqlite to keep the board).")
		return memoryRepo.NewMemoryRepo(), nil

	case "sheets":
		return openSheetsRepo(ctx)

	default:
		return nil, fmt.Errorf("unknown STORAGE %q (expected: sqlite, memory or sheets)", kind)
	}
}

func openSheetsRepo(ctx context.Context) (ports.BoardRepository, error) {
	cfg, err := sheetsConfig()
	if err != nil {
		return nil, err
	}
	repo, err := gsheetsRepo.NewSheetsRepo(ctx, cfg)
	if err != nil {
		return nil, err
	}
	fmt.Printf("Using Google Sheets storage: spreadsheet %s, tab %q\n", cfg.SpreadsheetID, repo.SheetName())
	return repo, nil
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
