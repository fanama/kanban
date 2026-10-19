package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"kanban-app/internal/domain"
	"kanban-app/internal/events"
	"kanban-app/internal/ports"
)

// boardService implements ports.BoardService.
type boardService struct {
	repo   ports.BoardRepository
	reader boardReader
	ids    *idGenerator
	pub    events.Publisher
}

func (s *boardService) ListBoards(ctx context.Context) ([]domain.Board, error) {
	return s.repo.ListBoards(ctx)
}

// CreateBoard appends a board: a new workspace with its own columns and cards.
func (s *boardService) CreateBoard(ctx context.Context, name string) (domain.Board, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.Board{}, domain.ErrEmptyBoardName
	}

	boards, err := s.repo.ListBoards(ctx)
	if err != nil {
		return domain.Board{}, err
	}
	for _, existing := range boards {
		if strings.EqualFold(existing.Name, name) {
			return domain.Board{}, fmt.Errorf("%w: %s", domain.ErrBoardExists, name)
		}
	}

	board := domain.Board{ID: s.ids.next("bd"), Name: name}
	if _, err := s.repo.CreateBoard(ctx, board); err != nil {
		return domain.Board{}, err
	}
	// A new board comes ready to use: the default stages To Do / In Progress /
	// Done are created with it.
	for _, column := range domain.DefaultColumns() {
		if _, err := s.repo.CreateColumn(ctx, board.ID, column); err != nil {
			return domain.Board{}, err
		}
	}
	publishBoardEvent(ctx, s.pub, events.BoardCreated, board)
	return board, nil
}

// DeleteBoard removes a board and everything on it — columns and cards alike.
// The last board stays: a blank state is an import (or a fresh storage) away.
func (s *boardService) DeleteBoard(ctx context.Context, id string) error {
	boards, err := s.repo.ListBoards(ctx)
	if err != nil {
		return err
	}
	if len(boards) <= 1 {
		return domain.ErrLastBoard
	}
	var name string
	for _, board := range boards {
		if board.ID == id {
			name = board.Name
		}
	}
	if err := s.repo.DeleteBoard(ctx, id); err != nil {
		return err
	}
	publishBoardEvent(ctx, s.pub, events.BoardDeleted, domain.Board{ID: id, Name: name})
	return nil
}

func publishBoardEvent(ctx context.Context, publisher events.Publisher, kind events.Kind, board domain.Board) {
	if publisher == nil {
		return
	}
	author := events.Actor(ctx)
	publisher.Publish(events.Event{
		Kind: kind, BoardID: board.ID, BoardName: board.Name,
		Actor: author, ClientID: events.ClientID(ctx), At: time.Now(),
		Notice: events.Notice{Kind: kind, Actor: author, BoardID: board.ID, BoardName: board.Name},
	})
}

// GetBoard returns one board's columns in order, each with its cards.
func (s *boardService) GetBoard(ctx context.Context, boardID string) (domain.BoardView, error) {
	return s.reader.get(ctx, boardID)
}
