package memory

import (
	"context"
	"fmt"

	"kanban-app/internal/domain"
)

// MoveCardBefore places a card immediately before another card of the same
// column. The memory store keeps one board-wide order; grouping by column
// preserves the relative order inside each column.
func (r *memoryRepo) MoveCardBefore(ctx context.Context, boardID, cardID, beforeCardID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	board, ok := r.state(boardID)
	if !ok || !board.cards[cardID] {
		return domain.ErrCardNotFound
	}
	if beforeCardID == "" {
		board.order = moveToEnd(board.order, cardID)
		return nil
	}
	if !board.cards[beforeCardID] {
		return fmt.Errorf("%w: %s", domain.ErrCardNotFound, beforeCardID)
	}
	if r.cards[cardID].ColumnID != r.cards[beforeCardID].ColumnID {
		return domain.ErrNotSameColumn
	}
	board.order = moveBefore(board.order, cardID, beforeCardID)
	return nil
}

func moveBefore(order []string, cardID, beforeCardID string) []string {
	from, to := indexOf(order, cardID), indexOf(order, beforeCardID)
	if from < 0 || to < 0 || from == to {
		return order
	}
	without := append(append([]string(nil), order[:from]...), order[from+1:]...)
	to = indexOf(without, beforeCardID)
	result := make([]string, 0, len(order))
	result = append(result, without[:to]...)
	result = append(result, cardID)
	return append(result, without[to:]...)
}

func moveToEnd(order []string, cardID string) []string {
	return append(removeID(order, cardID), cardID)
}

func removeID(order []string, cardID string) []string {
	from := indexOf(order, cardID)
	if from < 0 {
		return order
	}
	return append(append([]string(nil), order[:from]...), order[from+1:]...)
}

func indexOf(order []string, cardID string) int {
	for i, id := range order {
		if id == cardID {
			return i
		}
	}
	return -1
}
