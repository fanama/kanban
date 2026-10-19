package http

import "kanban-app/internal/domain"

// boardFacts is the single pass every card decoration reuses. Without it,
// domain.Unfinished/Dependents rebuilt the whole index once per card.
type boardFacts struct {
	board      domain.BoardView
	index      map[string]domain.Card
	positions  map[string]int
	dependents map[string][]domain.Card
}

func newBoardFacts(board domain.BoardView) *boardFacts {
	facts := &boardFacts{
		board:      board,
		index:      board.Index(),
		positions:  make(map[string]int, board.Count()),
		dependents: make(map[string][]domain.Card, board.Count()),
	}
	for _, column := range board.Columns {
		for _, card := range column.Cards {
			facts.positions[card.ID] = column.Column.Position
		}
	}
	for _, card := range board.All() {
		for _, id := range card.DependsOn {
			facts.dependents[id] = append(facts.dependents[id], card)
		}
	}
	return facts
}

func (f *boardFacts) unfinished(card domain.Card, targetPosition int) []domain.Card {
	blockers := make([]domain.Card, 0, len(card.DependsOn))
	for _, id := range card.DependsOn {
		dependency, ok := f.index[id]
		if !ok {
			blockers = append(blockers, domain.Card{ID: id, Title: id})
			continue
		}
		position, ok := f.positions[dependency.ID]
		if !ok || position < targetPosition-1 {
			blockers = append(blockers, dependency)
		}
	}
	return blockers
}

func (f *boardFacts) aheadOfDependents(card domain.Card, targetPosition int) []domain.Card {
	ahead := make([]domain.Card, 0)
	for _, dependent := range f.dependents[card.ID] {
		position, ok := f.positions[dependent.ID]
		if !ok || position < targetPosition {
			ahead = append(ahead, dependent)
		}
	}
	return ahead
}

func (f *boardFacts) hasDependents(card domain.Card) bool {
	return len(f.dependents[card.ID]) > 0
}

// canPull mirrors domain.PullPlan on the precomputed maps: a dependent that is
// behind the moved card must be pullable without breaking its own constraints.
func (f *boardFacts) canPull(card domain.Card, targetPosition int) bool {
	if targetPosition < 0 || targetPosition >= len(f.board.Columns) {
		return false
	}
	positions := make(map[string]int, len(f.positions))
	for id, position := range f.positions {
		positions[id] = position
	}
	positions[card.ID] = targetPosition

	queue := []domain.Card{card}
	seen := map[string]bool{card.ID: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		required := positions[current.ID]
		for _, dependent := range f.dependents[current.ID] {
			if seen[dependent.ID] || positions[dependent.ID] >= required {
				continue
			}
			if len(f.unfinishedAt(dependent, required, positions)) > 0 {
				return false
			}
			positions[dependent.ID] = required
			seen[dependent.ID] = true
			queue = append(queue, dependent)
		}
	}
	return true
}

// unfinishedAt is domain.Unfinished against a staged position map.
func (f *boardFacts) unfinishedAt(card domain.Card, targetPosition int, positions map[string]int) []domain.Card {
	blockers := make([]domain.Card, 0, len(card.DependsOn))
	for _, id := range card.DependsOn {
		dependency, ok := f.index[id]
		if !ok {
			blockers = append(blockers, domain.Card{ID: id, Title: id})
			continue
		}
		if position, ok := positions[dependency.ID]; !ok || position < targetPosition-1 {
			blockers = append(blockers, dependency)
		}
	}
	return blockers
}
