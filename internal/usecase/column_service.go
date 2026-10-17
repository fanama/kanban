package usecase

import (
	"context"
	"fmt"
	"strings"

	"kanban-app/internal/domain"
	"kanban-app/internal/ports"
)

// columnService implements ports.ColumnService.
type columnService struct {
	repo    ports.BoardRepository
	reader  boardReader
	ids     *idGenerator
	cascade cascader
}

// AddColumn appends a column at the end of the board.
func (s *columnService) AddColumn(ctx context.Context, boardID, name string) (domain.BoardView, error) {
	name = domain.CleanColumnName(name)
	if name == "" {
		return domain.BoardView{}, domain.ErrEmptyColumnName
	}

	board, err := s.reader.get(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}
	for _, column := range board.Columns {
		if strings.EqualFold(column.Column.Name, name) {
			return domain.BoardView{}, fmt.Errorf("%w: %s", domain.ErrColumnExists, name)
		}
	}

	column := domain.Column{ID: s.ids.next("col"), Name: name, Position: len(board.Columns)}
	if _, err := s.repo.CreateColumn(ctx, boardID, column); err != nil {
		return domain.BoardView{}, err
	}
	return s.reader.get(ctx, boardID)
}

// RenameColumn replaces a column's name: the column keeps its position and
// its cards, so nothing else on the board changes.
func (s *columnService) RenameColumn(ctx context.Context, boardID, columnID, name string) (domain.BoardView, error) {
	name = domain.CleanColumnName(name)
	if name == "" {
		return domain.BoardView{}, domain.ErrEmptyColumnName
	}

	board, err := s.reader.get(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}
	if _, ok := board.Column(columnID); !ok {
		return domain.BoardView{}, fmt.Errorf("%w: %s", domain.ErrColumnNotFound, columnID)
	}
	for _, column := range board.Columns {
		if column.Column.ID != columnID && strings.EqualFold(column.Column.Name, name) {
			return domain.BoardView{}, fmt.Errorf("%w: %s", domain.ErrColumnExists, name)
		}
	}

	if err := s.repo.RenameColumn(ctx, boardID, columnID, name); err != nil {
		return domain.BoardView{}, err
	}
	return s.reader.get(ctx, boardID)
}

// DeleteColumn removes a column and keeps its cards: they fall back to the
// previous stage (or to the column that becomes the first one when the deleted
// column is the first). The cards that depend on them follow, as always.
func (s *columnService) DeleteColumn(ctx context.Context, boardID, columnID string) (domain.BoardView, error) {
	board, err := s.reader.get(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}
	doomed, ok := board.Column(columnID)
	if !ok {
		return domain.BoardView{}, fmt.Errorf("%w: %s", domain.ErrColumnNotFound, columnID)
	}
	if len(board.Columns) < 2 {
		return domain.BoardView{}, domain.ErrLastColumn
	}
	fallback, ok := deleteFallback(board, doomed.Column)
	if !ok {
		return domain.BoardView{}, domain.ErrColumnNotFound
	}

	orphans := append([]domain.Card(nil), doomed.Cards...)
	if err := s.repo.DeleteColumn(ctx, boardID, columnID); err != nil {
		return domain.BoardView{}, err
	}
	if err := s.moveCards(ctx, boardID, orphans, fallback.ID); err != nil {
		return domain.BoardView{}, err
	}
	return s.settleOrphans(ctx, boardID, orphans)
}

// deleteFallback chooses the column receiving the cards of the deleted one:
// the previous stage, or the one that becomes first when the first is deleted.
func deleteFallback(board domain.BoardView, doomed domain.Column) (domain.Column, bool) {
	position := doomed.Position - 1
	if doomed.Position == 0 {
		position = 1
	}
	return board.ColumnAt(position)
}

func (s *columnService) moveCards(ctx context.Context, boardID string, cards []domain.Card, columnID string) error {
	for _, card := range cards {
		if err := s.repo.UpdateColumn(ctx, boardID, card.ID, columnID); err != nil {
			return err
		}
	}
	return nil
}

// settleOrphans re-reads the board for each moved card so each cascade starts
// from a fresh state (the surviving columns were renumbered).
func (s *columnService) settleOrphans(ctx context.Context, boardID string, orphans []domain.Card) (domain.BoardView, error) {
	for _, card := range orphans {
		after, err := s.reader.get(ctx, boardID)
		if err != nil {
			return domain.BoardView{}, err
		}
		position := after.Position(card.ID)
		if position < 0 {
			continue // already gone from the board: nothing to settle
		}
		if err := s.cascade.demote(ctx, boardID, after, card.ID, position); err != nil {
			return domain.BoardView{}, err
		}
	}
	return s.reader.get(ctx, boardID)
}
