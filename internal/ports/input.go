// Package ports declares the contracts between the application core and the
// outside world. Nothing here knows about HTTP, HTMX or SQLite.
package ports

import (
	"context"

	"kanban-app/internal/domain"
)

// BoardUseCase is the primary port: what a driver (HTTP, CLI, WebSocket) can
// ask the application to do. Every method returns the refreshed board, so a
// caller always renders one consistent state.
//
// A card or column method takes the board ID it applies to: the app may hold
// several boards.
type BoardUseCase interface {
	// Boards: the workspaces the app holds. The last board cannot be deleted
	// (ErrLastBoard); a storage that keeps a single board refuses to add
	// another (ErrBoardsUnsupported).
	ListBoards(ctx context.Context) ([]domain.Board, error)
	CreateBoard(ctx context.Context, name string) (domain.Board, error)
	DeleteBoard(ctx context.Context, id string) error

	GetBoard(ctx context.Context, boardID string) (domain.BoardView, error)
	AddCard(ctx context.Context, boardID, title string, priority domain.Priority) (domain.BoardView, error)
	UpdateCard(ctx context.Context, boardID string, card domain.Card) (domain.BoardView, error)
	MoveCard(ctx context.Context, boardID, cardID, columnID string) (domain.BoardView, error)

	// DeleteCard removes a card. Cards that depend on it keep their other
	// constraints; this one is dropped from their lists.
	DeleteCard(ctx context.Context, boardID, cardID string) (domain.BoardView, error)

	AddColumn(ctx context.Context, boardID, name string) (domain.BoardView, error)
	// RenameColumn replaces a column's name; the column keeps its position and
	// its cards.
	RenameColumn(ctx context.Context, boardID, columnID, name string) (domain.BoardView, error)
	DeleteColumn(ctx context.Context, boardID, columnID string) (domain.BoardView, error)

	// ExportBoard returns the named board as a snapshot that can be saved or
	// sent elsewhere. ImportBoard replaces that board's content with a
	// snapshot from such a file, after checking it against the same rules the
	// live board obeys.
	ExportBoard(ctx context.Context, boardID string) (domain.BoardSnapshot, error)
	ImportBoard(ctx context.Context, boardID string, snapshot domain.BoardSnapshot) (domain.BoardView, error)
}
