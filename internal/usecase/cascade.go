package usecase

import (
	"context"

	"kanban-app/internal/domain"
	"kanban-app/internal/ports"
)

// cascader applies the stage rule after a card changes column: a backward move
// demotes the cards that depend on it, a forward move pulls them along.
type cascader struct {
	repo   ports.BoardRepository
	reader boardReader
}

func (c cascader) afterMove(ctx context.Context, boardID string, board domain.BoardView, cardID string, from, to int) error {
	switch {
	case to < from:
		return c.demote(ctx, boardID, board, cardID, to)
	case to > from:
		return c.pull(ctx, boardID, board, cardID, to)
	default:
		return nil
	}
}

// demote moves every card that would end up more than one stage ahead of the
// card now sitting at position, transitively, so that a demoted card always
// drags the cards that depend on it down with it.
func (c cascader) demote(ctx context.Context, boardID string, board domain.BoardView, cardID string, position int) error {
	positions := make(map[string]int, board.Count())
	for _, column := range board.Columns {
		for _, card := range column.Cards {
			positions[card.ID] = column.Column.Position
		}
	}
	positions[cardID] = position

	card, ok := board.Index()[cardID]
	if !ok {
		return domain.ErrCardNotFound
	}
	card.ColumnID = columnIDAt(board, position)

	queue := []domain.Card{card}
	seen := map[string]bool{cardID: true}
	return c.demoteQueue(ctx, boardID, board, positions, queue, seen)
}

func (c cascader) demoteQueue(ctx context.Context, boardID string, board domain.BoardView, positions map[string]int, queue []domain.Card, seen map[string]bool) error {
	all := board.All()
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		maxAllowed := positions[current.ID] + 1
		for _, dependent := range domain.Dependents(current, all) {
			if seen[dependent.ID] || positions[dependent.ID] <= maxAllowed {
				continue
			}

			target, ok := board.ColumnAt(maxAllowed)
			if !ok {
				continue
			}
			if err := c.repo.UpdateColumn(ctx, boardID, dependent.ID, target.ID); err != nil {
				return err
			}

			positions[dependent.ID] = target.Position
			seen[dependent.ID] = true
			dependent.ColumnID = target.ID
			queue = append(queue, dependent)
		}
	}
	return nil
}

// pull carries forward every card that would be left behind by the card now
// sitting at position — the mirror of demote. The plan is computed by the
// domain (pure logic) before anything is written: it is refused when a
// dependent cannot be brought along without breaking its own stage rule.
func (c cascader) pull(ctx context.Context, boardID string, board domain.BoardView, cardID string, position int) error {
	plan, err := domain.PullPlan(*mustCard(board, cardID), position, board)
	if err != nil {
		return err
	}
	for _, step := range plan {
		if err := c.repo.UpdateColumn(ctx, boardID, step.CardID, step.TargetColumn.ID); err != nil {
			return err
		}
	}
	return nil
}

func mustCard(board domain.BoardView, cardID string) *domain.Card {
	card, ok := board.Index()[cardID]
	if !ok {
		return &domain.Card{ID: cardID}
	}
	return &card
}

func columnIDAt(board domain.BoardView, position int) string {
	if column, ok := board.ColumnAt(position); ok {
		return column.ID
	}
	return ""
}
