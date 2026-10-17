package usecase

import (
	"context"
	"fmt"
	"strings"

	"kanban-app/internal/domain"
	"kanban-app/internal/ports"
)

// cardService implements ports.CardService.
type cardService struct {
	repo    ports.BoardRepository
	reader  boardReader
	ids     *idGenerator
	cascade cascader
}

// AddCard appends a new card to a column. An empty columnID keeps the
// historical behavior (the board's first column); a new card has no
// constraints yet, so any existing column is legal.
func (s *cardService) AddCard(ctx context.Context, boardID, columnID, title string, priority domain.Priority) (domain.BoardView, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return domain.BoardView{}, domain.ErrEmptyTitle
	}

	board, err := s.reader.get(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}
	target, err := targetColumn(board, columnID)
	if err != nil {
		return domain.BoardView{}, err
	}

	card := domain.Card{
		ID:       s.ids.next("card"),
		Title:    title,
		ColumnID: target.ID,
		Priority: priority.Normalize(),
	}
	if _, err := s.repo.Create(ctx, boardID, card); err != nil {
		return domain.BoardView{}, err
	}
	return s.reader.get(ctx, boardID)
}

// targetColumn resolves the destination column: the named one, or the first
// column when the caller did not choose.
func targetColumn(board domain.BoardView, columnID string) (domain.Column, error) {
	if columnID == "" {
		first, ok := board.ColumnAt(0)
		if ok {
			return first, nil
		}
	} else if view, ok := board.Column(columnID); ok {
		return view.Column, nil
	}
	return domain.Column{}, fmt.Errorf("%w: the board has no column to add cards to", domain.ErrColumnNotFound)
}

// DeleteCard removes a card. Cards that depend on it keep their other
// constraints: the deleted one is dropped from their lists, so nobody keeps
// waiting for a card that is no longer there.
func (s *cardService) DeleteCard(ctx context.Context, boardID, cardID string) (domain.BoardView, error) {
	board, err := s.reader.get(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}

	card, ok := board.Index()[cardID]
	if !ok {
		return domain.BoardView{}, domain.ErrCardNotFound
	}

	for _, dependent := range domain.Dependents(card, board.All()) {
		kept := make([]string, 0, len(dependent.DependsOn))
		for _, dep := range dependent.DependsOn {
			if dep != cardID {
				kept = append(kept, dep)
			}
		}
		updated := dependent
		updated.DependsOn = kept
		if err := s.repo.Update(ctx, boardID, updated); err != nil {
			return domain.BoardView{}, err
		}
	}

	if err := s.repo.DeleteCard(ctx, boardID, cardID); err != nil {
		return domain.BoardView{}, err
	}
	return s.reader.get(ctx, boardID)
}

// MoveCard moves a card to a column, enforcing the stage rule. Going forward,
// the cards that depend on it are pulled along (a constraint never passes its
// dependents); going backward, those same cards are demoted with it.
func (s *cardService) MoveCard(ctx context.Context, boardID, cardID, columnID string) (domain.BoardView, error) {
	board, err := s.reader.get(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}

	card, ok := board.Index()[cardID]
	if !ok {
		return domain.BoardView{}, domain.ErrCardNotFound
	}
	target, ok := board.Column(columnID)
	if !ok {
		return domain.BoardView{}, fmt.Errorf("%w: %s", domain.ErrColumnNotFound, columnID)
	}

	from := board.Position(card.ID)
	if from == target.Column.Position {
		return board, nil
	}
	if target.Column.Position > from {
		if err := checkStage(card, target.Column, board); err != nil {
			return domain.BoardView{}, err
		}
	}
	if err := s.moveTo(ctx, boardID, card.ID, target.Column.ID); err != nil {
		return domain.BoardView{}, err
	}
	if err := s.cascade.afterMove(ctx, boardID, board, card.ID, from, target.Column.Position); err != nil {
		return domain.BoardView{}, err
	}
	return s.reader.get(ctx, boardID)
}

// UpdateCard saves an edited card. Text fields left empty by the caller keep
// their stored value. A nil constraint list also keeps the stored constraints
// (a partial edit must not drop them); a non-nil list — even an empty one —
// replaces them, so sending [] removes every constraint.
func (s *cardService) UpdateCard(ctx context.Context, boardID string, edit domain.Card) (domain.BoardView, error) {
	board, err := s.reader.get(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}

	before, ok := board.Index()[edit.ID]
	if !ok {
		return domain.BoardView{}, domain.ErrCardNotFound
	}
	from := board.Position(before.ID)

	updated, err := merge(edit, before)
	if err != nil {
		return domain.BoardView{}, err
	}
	if err := validateDependencies(board.All(), updated); err != nil {
		return domain.BoardView{}, err
	}

	to, ok := board.Column(updated.ColumnID)
	if !ok {
		return domain.BoardView{}, fmt.Errorf("%w: %s", domain.ErrColumnNotFound, updated.ColumnID)
	}
	if to.Column.Position > from {
		if err := checkStage(updated, to.Column, board); err != nil {
			return domain.BoardView{}, err
		}
	}

	if err := s.repo.Update(ctx, boardID, updated); err != nil {
		return domain.BoardView{}, err
	}
	if err := s.cascade.afterMove(ctx, boardID, board, updated.ID, from, to.Column.Position); err != nil {
		return domain.BoardView{}, err
	}
	return s.reader.get(ctx, boardID)
}

func (s *cardService) moveTo(ctx context.Context, boardID, cardID, columnID string) error {
	return s.repo.UpdateColumn(ctx, boardID, cardID, columnID)
}
