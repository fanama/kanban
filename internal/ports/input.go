// Package ports declares the contracts between the application core and the
// outside world. Nothing here knows about HTTP, HTMX or SQLite.
package ports

import (
	"context"

	"kanban-app/internal/domain"
)

// BoardService is the workspace side of the input port: listing, creating and
// deleting boards, and reading one board with its columns and cards.
//
// Every mutating service returns the refreshed board, so a caller always
// renders one consistent state. A card or column method takes the board ID it
// applies to: the app may hold several boards.
type BoardService interface {
	// ListBoards returns the boards the storage holds.
	ListBoards(ctx context.Context) ([]domain.Board, error)
	// CreateBoard appends a board with its default columns. A storage that
	// keeps a single board refuses another (ErrBoardsUnsupported).
	CreateBoard(ctx context.Context, name string) (domain.Board, error)
	// DeleteBoard removes a board and everything on it. The last board cannot
	// be deleted (ErrLastBoard).
	DeleteBoard(ctx context.Context, id string) error
	GetBoard(ctx context.Context, boardID string) (domain.BoardView, error)
}

// CardService owns the card lifecycle. AddCard targets a column; an empty
// columnID keeps the historical behavior (the board's first column).
type CardService interface {
	AddCard(ctx context.Context, boardID, columnID, title string, priority domain.Priority) (domain.BoardView, error)
	UpdateCard(ctx context.Context, boardID string, card domain.Card) (domain.BoardView, error)
	MoveCard(ctx context.Context, boardID, cardID, columnID string) (domain.BoardView, error)
	// DeleteCard removes a card. Cards that depend on it keep their other
	// constraints; this one is dropped from their lists.
	DeleteCard(ctx context.Context, boardID, cardID string) (domain.BoardView, error)
}

// ColumnService owns the board's stages. Deleting a column moves its cards to
// the previous stage (the first one when the deleted column was first) and the
// last column is protected (ErrLastColumn).
type ColumnService interface {
	AddColumn(ctx context.Context, boardID, name string) (domain.BoardView, error)
	// RenameColumn replaces a column's name; the column keeps its position and
	// its cards.
	RenameColumn(ctx context.Context, boardID, columnID, name string) (domain.BoardView, error)
	DeleteColumn(ctx context.Context, boardID, columnID string) (domain.BoardView, error)
}

// ImportExportService moves one board in and out of the app as a snapshot.
type ImportExportService interface {
	// ExportBoard returns the named board as a snapshot that can be saved or
	// sent elsewhere.
	ExportBoard(ctx context.Context, boardID string) (domain.BoardSnapshot, error)
	// ImportBoard replaces that board's content with a snapshot from such a
	// file, after checking it against the same rules the live board obeys.
	ImportBoard(ctx context.Context, boardID string, snapshot domain.BoardSnapshot) (domain.BoardView, error)
}
