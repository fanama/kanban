package usecase

import (
	"context"
	"time"

	"kanban-app/internal/domain"
	"kanban-app/internal/events"
	"kanban-app/internal/ports"
)

// changeFor builds the patch description from the before/after boards and
// fills the notice with the request identity.
func changeFor(ctx context.Context, kind events.Kind, boardID string, before, after domain.BoardView, notice events.Notice) ports.BoardChange {
	change := diffChange(before, after, kind, notice)
	change.Notice.Actor = events.Actor(ctx)
	change.Notice.BoardID = boardID
	return change
}

// publishChange sends the observer notification. A publish failure is never
// allowed to turn a committed mutation into an error.
func publishChange(ctx context.Context, publisher events.Publisher, change ports.BoardChange) {
	if publisher == nil {
		return
	}
	publisher.Publish(events.Event{
		Kind:           change.Kind,
		BoardID:        change.Notice.BoardID,
		Actor:          change.Notice.Actor,
		ClientID:       events.ClientID(ctx),
		CardIDs:        change.CardIDs,
		ColumnIDs:      change.ColumnIDs,
		RemovedCards:   change.RemovedCards,
		RemovedColumns: change.RemovedColumns,
		Reload:         change.Reload,
		Notice:         change.Notice,
		At:             time.Now(),
	})
}

func diffChange(before, after domain.BoardView, kind events.Kind, notice events.Notice) ports.BoardChange {
	change := ports.BoardChange{Board: after, Kind: kind, Notice: notice}
	changedCards(&change, before, after)
	changedColumns(&change, before, after)
	affectedColumns(&change, before, after)
	return change
}

func changedCards(change *ports.BoardChange, before, after domain.BoardView) {
	old := before.Index()
	now := after.Index()
	for _, card := range after.All() {
		previous, existed := old[card.ID]
		if !existed {
			change.CardIDs = appendUnique(change.CardIDs, card.ID)
			continue
		}
		if !cardsEqual(previous, card) {
			change.CardIDs = appendUnique(change.CardIDs, card.ID)
		}
	}
	for _, card := range before.All() {
		if _, stillThere := now[card.ID]; !stillThere {
			change.RemovedCards = appendUnique(change.RemovedCards, card.ID)
		}
	}
}

func changedColumns(change *ports.BoardChange, before, after domain.BoardView) {
	old := columnMap(before)
	now := columnMap(after)
	for id, column := range now {
		previous, existed := old[id]
		if !existed || previous.Name != column.Name {
			change.ColumnIDs = appendUnique(change.ColumnIDs, id)
		}
	}
	for id := range old {
		if _, stillThere := now[id]; !stillThere {
			change.RemovedColumns = appendUnique(change.RemovedColumns, id)
		}
	}
}

// affectedColumns adds the source and destination columns of every changed or
// removed card, so the patch refreshes their counts and card lists.
func affectedColumns(change *ports.BoardChange, before, after domain.BoardView) {
	for _, id := range append(append([]string{}, change.CardIDs...), change.RemovedCards...) {
		if column, ok := before.ColumnAt(before.Position(id)); ok && before.Position(id) >= 0 {
			change.ColumnIDs = appendUnique(change.ColumnIDs, column.ID)
		}
		if column, ok := after.ColumnAt(after.Position(id)); ok && after.Position(id) >= 0 {
			change.ColumnIDs = appendUnique(change.ColumnIDs, column.ID)
		}
	}
}

func cardsEqual(a, b domain.Card) bool {
	if a.Title != b.Title || a.Description != b.Description || a.Objective != b.Objective ||
		a.ColumnID != b.ColumnID || a.Priority != b.Priority || a.Version != b.Version ||
		len(a.DependsOn) != len(b.DependsOn) {
		return false
	}
	for i := range a.DependsOn {
		if a.DependsOn[i] != b.DependsOn[i] {
			return false
		}
	}
	return true
}

func columnMap(board domain.BoardView) map[string]domain.Column {
	columns := make(map[string]domain.Column, len(board.Columns))
	for _, column := range board.Columns {
		columns[column.Column.ID] = column.Column
	}
	return columns
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
