package usecase

import (
	"fmt"
	"strings"

	"kanban-app/internal/domain"
)

// checkStage enforces "a card can only move to column n+1 when its constraints
// are at stage n or further".
func checkStage(card domain.Card, target domain.Column, board domain.BoardView) error {
	blockers := domain.Unfinished(card, target.Position, board)
	if len(blockers) == 0 {
		return nil
	}

	required := target.Position - 1
	if required < 0 {
		required = 0
	}
	if column, ok := board.ColumnAt(required); ok {
		return fmt.Errorf("%w: to reach %q, %s must be at %q first",
			domain.ErrCardBlocked, target.Name, titles(blockers), column.Name)
	}
	return fmt.Errorf("%w: %s", domain.ErrCardBlocked, titles(blockers))
}

// merge overlays the edited fields on the stored card and enforces the rules:
// a title is mandatory and the column must exist.
func merge(edit, current domain.Card) (domain.Card, error) {
	updated := current
	updated.Title = strings.TrimSpace(edit.Title)
	if updated.Title == "" {
		return domain.Card{}, domain.ErrEmptyTitle
	}

	updated.Description = strings.TrimSpace(edit.Description)
	updated.Objective = strings.TrimSpace(edit.Objective)
	if edit.DependsOn != nil {
		updated.DependsOn = cleanIDs(edit.DependsOn)
	}
	if edit.ColumnID != "" {
		updated.ColumnID = strings.TrimSpace(edit.ColumnID)
	}
	if edit.Priority != "" {
		updated.Priority = edit.Priority.Normalize()
	}

	return updated, nil
}

// cleanIDs trims the constraint list, drops blanks and duplicates, and keeps
// the order the caller used. The result stays non-nil so an empty list means
// "no constraints", which is different from nil ("leave them alone").
func cleanIDs(ids []string) []string {
	cleaned := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		cleaned = append(cleaned, id)
	}
	return cleaned
}

// validateDependencies checks that a card's constraints exist, are not itself
// and do not create a cycle.
func validateDependencies(cards []domain.Card, card domain.Card) error {
	index := make(map[string]domain.Card, len(cards))
	for _, c := range cards {
		index[c.ID] = c
	}

	for _, id := range card.DependsOn {
		if id == card.ID {
			return domain.ErrSelfDependency
		}
		if _, ok := index[id]; !ok {
			return fmt.Errorf("%w: %s", domain.ErrUnknownDependency, id)
		}
	}

	index[card.ID] = card
	if createsCycle(card, index) {
		return domain.ErrDependencyCycle
	}
	return nil
}

// createsCycle walks the constraints of card: coming back to card means the
// graph would no longer be a DAG, so no card could ever be finished.
func createsCycle(card domain.Card, index map[string]domain.Card) bool {
	visited := map[string]bool{}

	var walk func(id string) bool
	walk = func(id string) bool {
		if id == card.ID {
			return true
		}
		if visited[id] {
			return false
		}
		visited[id] = true
		for _, next := range index[id].DependsOn {
			if walk(next) {
				return true
			}
		}
		return false
	}

	for _, dep := range card.DependsOn {
		if walk(dep) {
			return true
		}
	}
	return false
}

func titles(cards []domain.Card) string {
	names := make([]string, 0, len(cards))
	for _, c := range cards {
		names = append(names, fmt.Sprintf("%q", c.Title))
	}
	return strings.Join(names, ", ")
}
