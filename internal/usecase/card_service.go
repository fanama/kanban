package usecase

import (
	"context"
	"fmt"
	"strings"

	"kanban-app/internal/domain"
	"kanban-app/internal/events"
	"kanban-app/internal/ports"
)

// cardService implements ports.CardService.
type cardService struct {
	repo    ports.BoardRepository
	reader  boardReader
	ids     *idGenerator
	cascade cascader
	pub     events.Publisher
}

// AddCard appends a new card to a column. An empty columnID keeps the
// historical behavior (the board's first column); a new card has no
// constraints yet, so any existing column is legal.
func (s *cardService) AddCard(ctx context.Context, boardID, columnID, title string, priority domain.Priority) (ports.BoardChange, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return ports.BoardChange{}, domain.ErrEmptyTitle
	}
	board, target, err := s.addTarget(ctx, boardID, columnID)
	if err != nil {
		return ports.BoardChange{}, err
	}
	card := domain.Card{
		ID: s.ids.next("card"), Title: title, ColumnID: target.ID,
		Priority: priority.Normalize(), Version: 1,
	}
	if _, err := s.repo.Create(ctx, boardID, card); err != nil {
		return ports.BoardChange{}, err
	}
	return s.afterCardChange(ctx, events.CardAdded, boardID, board, events.Notice{
		Kind: events.CardAdded, CardID: card.ID, CardTitle: card.Title,
		ColumnID: target.ID, ColumnName: target.Name, To: target.Name,
	})
}

func (s *cardService) addTarget(ctx context.Context, boardID, columnID string) (domain.BoardView, domain.Column, error) {
	board, err := s.reader.get(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, domain.Column{}, err
	}
	target, err := targetColumn(board, columnID)
	if err != nil {
		return domain.BoardView{}, domain.Column{}, err
	}
	return board, target, nil
}

// DeleteCard removes a card. Cards that depend on it keep their other
// constraints: the deleted one is dropped from their lists.
func (s *cardService) DeleteCard(ctx context.Context, boardID, cardID string) (ports.BoardChange, error) {
	board, err := s.reader.get(ctx, boardID)
	if err != nil {
		return ports.BoardChange{}, err
	}
	card, ok := board.Index()[cardID]
	if !ok {
		return ports.BoardChange{}, domain.ErrCardNotFound
	}
	column, _ := board.ColumnAt(board.Position(cardID))
	if err := s.dropFromDependents(ctx, boardID, card, board); err != nil {
		return ports.BoardChange{}, err
	}
	if err := s.repo.DeleteCard(ctx, boardID, cardID); err != nil {
		return ports.BoardChange{}, err
	}
	return s.afterCardChange(ctx, events.CardDeleted, boardID, board, events.Notice{
		Kind: events.CardDeleted, CardID: cardID, CardTitle: card.Title,
		ColumnID: column.ID, ColumnName: column.Name, From: column.Name,
	})
}

func (s *cardService) dropFromDependents(ctx context.Context, boardID string, card domain.Card, board domain.BoardView) error {
	for _, dependent := range domain.Dependents(card, board.All()) {
		kept := make([]string, 0, len(dependent.DependsOn))
		for _, dep := range dependent.DependsOn {
			if dep != card.ID {
				kept = append(kept, dep)
			}
		}
		dependent.DependsOn = kept
		if err := s.repo.Update(ctx, boardID, dependent); err != nil {
			return err
		}
	}
	return nil
}

// MoveCard moves a card to a column, enforcing the stage rule. Going forward,
// the cards that depend on it are pulled along (a constraint never passes its
// dependents); going backward, those same cards are demoted with it.
func (s *cardService) MoveCard(ctx context.Context, boardID, cardID, columnID string) (ports.BoardChange, error) {
	board, err := s.reader.get(ctx, boardID)
	if err != nil {
		return ports.BoardChange{}, err
	}
	card, ok := board.Index()[cardID]
	if !ok {
		return ports.BoardChange{}, domain.ErrCardNotFound
	}
	target, ok := board.Column(columnID)
	if !ok {
		return ports.BoardChange{}, fmt.Errorf("%w: %s", domain.ErrColumnNotFound, columnID)
	}
	from := board.Position(card.ID)
	if from == target.Column.Position {
		return ports.BoardChange{Board: board, Kind: events.CardMoved}, nil
	}
	if err := s.applyMove(ctx, boardID, board, card, target.Column, from); err != nil {
		return ports.BoardChange{}, err
	}
	return s.afterCardChange(ctx, events.CardMoved, boardID, board, events.Notice{
		Kind: events.CardMoved, CardID: cardID, CardTitle: card.Title,
		ColumnID: target.Column.ID, ColumnName: target.Column.Name,
		From: board.Columns[from].Column.Name, To: target.Column.Name,
	})
}

// ReorderCard moves a card immediately before another card of the same column.
// The stage rules do not change, so this is a pure display-order operation.
func (s *cardService) ReorderCard(ctx context.Context, boardID, cardID, beforeCardID string) (ports.BoardChange, error) {
	board, err := s.reader.get(ctx, boardID)
	if err != nil {
		return ports.BoardChange{}, err
	}
	card, _, err := reorderTargets(board, cardID, beforeCardID)
	if err != nil {
		return ports.BoardChange{}, err
	}
	if beforeCardID != "" && cardID == beforeCardID {
		return ports.BoardChange{Board: board, Kind: events.CardMoved}, nil
	}
	if err := s.repo.MoveCardBefore(ctx, boardID, cardID, beforeCardID); err != nil {
		return ports.BoardChange{}, err
	}
	return s.afterReorder(ctx, boardID, board, card)
}

func reorderTargets(board domain.BoardView, cardID, beforeCardID string) (domain.Card, domain.Card, error) {
	index := board.Index()
	card, ok := index[cardID]
	if !ok {
		return domain.Card{}, domain.Card{}, domain.ErrCardNotFound
	}
	if beforeCardID == "" {
		return card, domain.Card{}, nil // append at the end of the card's column
	}
	before, ok := index[beforeCardID]
	if !ok {
		return domain.Card{}, domain.Card{}, fmt.Errorf("%w: %s", domain.ErrCardNotFound, beforeCardID)
	}
	if cardID != beforeCardID && card.ColumnID != before.ColumnID {
		return domain.Card{}, domain.Card{}, fmt.Errorf("%w: %q and %q", domain.ErrNotSameColumn, card.Title, before.Title)
	}
	return card, before, nil
}

func (s *cardService) afterReorder(ctx context.Context, boardID string, board domain.BoardView, card domain.Card) (ports.BoardChange, error) {
	after, err := s.reader.get(ctx, boardID)
	if err != nil {
		return ports.BoardChange{}, err
	}
	column, _ := after.Column(card.ColumnID)
	change := changeFor(ctx, events.CardMoved, boardID, board, after, events.Notice{
		Kind: events.CardMoved, CardID: card.ID, CardTitle: card.Title,
		ColumnID: card.ColumnID, ColumnName: column.Column.Name,
		From: column.Column.Name, To: column.Column.Name,
	})
	change.CardIDs = appendUnique(change.CardIDs, card.ID)
	change.ColumnIDs = appendUnique(change.ColumnIDs, card.ColumnID)
	publishChange(ctx, s.pub, change)
	return change, nil
}

func (s *cardService) applyMove(ctx context.Context, boardID string, board domain.BoardView, card domain.Card, target domain.Column, from int) error {
	if target.Position > from {
		if err := checkStage(card, target, board); err != nil {
			return err
		}
	}
	if err := s.moveTo(ctx, boardID, card.ID, target.ID); err != nil {
		return err
	}
	return s.cascade.afterMove(ctx, boardID, board, card.ID, from, target.Position)
}

// UpdateCard saves an edited card. A nil constraint list keeps the stored
// constraints; a non-nil list, even an empty one, replaces them.
func (s *cardService) UpdateCard(ctx context.Context, boardID string, edit domain.Card) (ports.BoardChange, error) {
	board, err := s.reader.get(ctx, boardID)
	if err != nil {
		return ports.BoardChange{}, err
	}
	before, ok := board.Index()[edit.ID]
	if !ok {
		return ports.BoardChange{}, domain.ErrCardNotFound
	}
	from := board.Position(before.ID)
	updated, to, err := s.applyEdit(ctx, boardID, board, before, edit, from)
	if err != nil {
		return ports.BoardChange{}, err
	}
	kind := events.CardUpdated
	if to.Column.Position != from {
		kind = events.CardMoved
	}
	return s.afterCardChange(ctx, kind, boardID, board, events.Notice{
		Kind: kind, CardID: updated.ID, CardTitle: updated.Title,
		ColumnID: to.Column.ID, ColumnName: to.Column.Name, To: to.Column.Name,
		From: board.Columns[from].Column.Name,
	})
}

func (s *cardService) applyEdit(ctx context.Context, boardID string, board domain.BoardView, before, edit domain.Card, from int) (domain.Card, domain.ColumnView, error) {
	if edit.Version > 0 && edit.Version != before.Version {
		return domain.Card{}, domain.ColumnView{}, fmt.Errorf("%w: %s", domain.ErrCardConflict, before.Title)
	}
	updated, err := merge(edit, before)
	if err != nil {
		return domain.Card{}, domain.ColumnView{}, err
	}
	updated.Version = before.Version // the repository advances it atomically
	if err := validateDependencies(board.All(), updated); err != nil {
		return domain.Card{}, domain.ColumnView{}, err
	}
	to, ok := board.Column(updated.ColumnID)
	if !ok {
		return domain.Card{}, domain.ColumnView{}, fmt.Errorf("%w: %s", domain.ErrColumnNotFound, updated.ColumnID)
	}
	if to.Column.Position > from {
		if err := checkStage(updated, to.Column, board); err != nil {
			return domain.Card{}, domain.ColumnView{}, err
		}
	}
	if err := s.repo.Update(ctx, boardID, updated); err != nil {
		return domain.Card{}, domain.ColumnView{}, err
	}
	if err := s.cascade.afterMove(ctx, boardID, board, updated.ID, from, to.Column.Position); err != nil {
		return domain.Card{}, domain.ColumnView{}, err
	}
	return updated, to, nil
}

// afterCardChange re-reads the board, builds the targeted change and publishes
// the observer event.
func (s *cardService) afterCardChange(ctx context.Context, kind events.Kind, boardID string, before domain.BoardView, notice events.Notice) (ports.BoardChange, error) {
	after, err := s.reader.get(ctx, boardID)
	if err != nil {
		return ports.BoardChange{}, err
	}
	change := changeFor(ctx, kind, boardID, before, after, notice)
	publishChange(ctx, s.pub, change)
	return change, nil
}

func (s *cardService) moveTo(ctx context.Context, boardID, cardID, columnID string) error {
	return s.repo.UpdateColumn(ctx, boardID, cardID, columnID)
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
