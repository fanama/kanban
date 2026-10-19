package ports

import (
	"context"

	"kanban-app/internal/domain"
)

// BoardRepository is the secondary port: what the application core needs from
// storage. Implemented by the memory, SQLite and Google Sheets adapters.
//
// The repository stores and returns exactly what it is given; the rules (the
// stage gate, the demotion cascade, which column receives the cards of a deleted
// column) live in the use cases, not here.
//
// Cards and columns are scoped to a board: a card of board A is invisible from
// board B.
type BoardRepository interface {
	// Boards: the workspaces held by the storage. The last board cannot be
	// deleted; a single-board storage refuses to add another.
	ListBoards(ctx context.Context) ([]domain.Board, error)
	CreateBoard(ctx context.Context, board domain.Board) (domain.Board, error)
	DeleteBoard(ctx context.Context, id string) error

	GetAll(ctx context.Context, boardID string) ([]domain.Card, error)
	GetByID(ctx context.Context, boardID, id string) (domain.Card, error)
	Create(ctx context.Context, boardID string, card domain.Card) (domain.Card, error)
	Update(ctx context.Context, boardID string, card domain.Card) error
	UpdateColumn(ctx context.Context, boardID, cardID, columnID string) error
	// MoveCardBefore places cardID immediately before beforeCardID in the same
	// column; an empty beforeCardID appends it at the end of its column.
	// Storage order is what the board renders, so this only rewrites positions;
	// neither card's version or fields change.
	MoveCardBefore(ctx context.Context, boardID, cardID, beforeCardID string) error

	DeleteCard(ctx context.Context, boardID, cardID string) error

	GetColumns(ctx context.Context, boardID string) ([]domain.Column, error)
	CreateColumn(ctx context.Context, boardID string, column domain.Column) (domain.Column, error)
	RenameColumn(ctx context.Context, boardID, id, name string) error
	DeleteColumn(ctx context.Context, boardID, id string) error

	// ReplaceBoard swaps the whole content of one board for the given columns
	// and cards: it is how a board file is loaded (the use case has already
	// validated the snapshot). Nothing outside the import path may call it.
	ReplaceBoard(ctx context.Context, boardID string, columns []domain.Column, cards []domain.Card) error
}
