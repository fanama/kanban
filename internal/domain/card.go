// Package domain holds the core business entities and rules.
// It has zero external dependencies: no HTTP, no database, no framework.
package domain

import (
	"fmt"
	"strconv"
	"strings"
)

type Priority string

const (
	PriorityLow    Priority = "Low"
	PriorityMedium Priority = "Medium"
	PriorityHigh   Priority = "High"
)

// Card is a single item on the board. Title is the only required text: the
// other fields carry the work item's content and may be empty.
//
// ColumnID places the card on the board, DependsOn lists the cards that
// constrain it.
type Card struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Objective   string   `json:"objective"`
	DependsOn   []string `json:"depends_on"`
	ColumnID    string   `json:"column_id"`
	Priority    Priority `json:"priority"`
}

// IsValid reports whether the priority is one the domain accepts.
func (p Priority) IsValid() bool {
	switch p {
	case PriorityLow, PriorityMedium, PriorityHigh:
		return true
	default:
		return false
	}
}

// Normalize falls back to Low when the value is unknown.
func (p Priority) Normalize() Priority {
	if p.IsValid() {
		return p
	}
	return PriorityLow
}

// ColumnView is one column of the board and the cards it holds.
type ColumnView struct {
	Column Column
	Cards  []Card
}

// BoardView is the read model: the columns in order, each with its cards.
type BoardView struct {
	Columns []ColumnView
}

// All returns every card of the board, column by column.
func (b BoardView) All() []Card {
	all := make([]Card, 0, b.Count())
	for _, column := range b.Columns {
		all = append(all, column.Cards...)
	}
	return all
}

// Count returns the number of cards on the board.
func (b BoardView) Count() int {
	n := 0
	for _, column := range b.Columns {
		n += len(column.Cards)
	}
	return n
}

// Index returns the board's cards keyed by ID.
func (b BoardView) Index() map[string]Card {
	index := make(map[string]Card, b.Count())
	for _, column := range b.Columns {
		for _, c := range column.Cards {
			index[c.ID] = c
		}
	}
	return index
}

// Columns returns the board's columns in order.
func (b BoardView) OrderedColumns() []Column {
	columns := make([]Column, 0, len(b.Columns))
	for _, column := range b.Columns {
		columns = append(columns, column.Column)
	}
	return columns
}

// Column returns a column and the cards it holds.
func (b BoardView) Column(id string) (ColumnView, bool) {
	for _, column := range b.Columns {
		if column.Column.ID == id {
			return column, true
		}
	}
	return ColumnView{}, false
}

// ColumnAt returns the column sitting at a position, clamped to the board: a
// position below zero gives the first column, a position past the end gives the
// last one. It reports false only on an empty board.
func (b BoardView) ColumnAt(position int) (Column, bool) {
	if len(b.Columns) == 0 {
		return Column{}, false
	}
	if position < 0 {
		position = 0
	}
	if position >= len(b.Columns) {
		position = len(b.Columns) - 1
	}
	return b.Columns[position].Column, true
}

// Position returns the position of a card's column, or -1 when the card is not
// on the board.
func (b BoardView) Position(cardID string) int {
	for _, column := range b.Columns {
		for _, c := range column.Cards {
			if c.ID == cardID {
				return column.Column.Position
			}
		}
	}
	return -1
}

// Unfinished returns the dependencies of c that keep it out of the column at
// targetPosition: a card may only sit in column n when its constraints are at
// stage n-1 or further, so anything earlier than targetPosition-1 blocks it. An
// unknown dependency blocks too (the rule fails closed), and the first column
// is always reachable.
func Unfinished(c Card, targetPosition int, b BoardView) []Card {
	index := b.Index()
	blockers := make([]Card, 0, len(c.DependsOn))
	for _, id := range c.DependsOn {
		dep, ok := index[id]
		if !ok {
			blockers = append(blockers, Card{ID: id, Title: id})
			continue
		}
		if pos := b.Position(dep.ID); pos < 0 || pos < targetPosition-1 {
			blockers = append(blockers, dep)
		}
	}
	return blockers
}

// Dependents returns the cards that declare c as a constraint, in board order.
func Dependents(c Card, all []Card) []Card {
	dependents := make([]Card, 0)
	for _, other := range all {
		for _, id := range other.DependsOn {
			if id == c.ID {
				dependents = append(dependents, other)
				break
			}
		}
	}
	return dependents
}

// AheadOfDependents returns the cards that declare c as a constraint and would
// fall behind it if c moved to targetPosition: a constraint never passes the
// cards that depend on it — "card X awaits c" means nothing once c has gone.
// A dependent strictly before the target keeps the move from happening; the
// rule fails closed, so a dependent whose column is unknown counts too.
func AheadOfDependents(c Card, targetPosition int, b BoardView) []Card {
	ahead := make([]Card, 0)
	for _, dependent := range Dependents(c, b.All()) {
		if pos := b.Position(dependent.ID); pos < 0 || pos < targetPosition {
			ahead = append(ahead, dependent)
		}
	}
	return ahead
}

// DependsOnCard reports whether c depends on the card with the given ID.
func (c Card) DependsOnCard(id string) bool {
	for _, dep := range c.DependsOn {
		if dep == id {
			return true
		}
	}
	return false
}

// PullStep is one card move planned by PullPlan: the card with the given ID
// should be carried to the column at the given position.
type PullStep struct {
	CardID       string
	TargetColumn Column
}

// PullPlan computes the moves needed to keep "a constraint never passes its
// dependents" when c moves forward to targetPosition: every card depending on
// c — transitively — is carried to the stage c has reached, unless its own
// constraints have not reached it, in which case the plan reports an error: the
// move must be refused, since c could not be pulled along either.
func PullPlan(c Card, targetPosition int, b BoardView) ([]PullStep, error) {
	positions := cardPositions(b)
	positions[c.ID] = targetPosition

	moved := c
	moved.ColumnID = b.Columns[targetPosition].Column.ID
	return pullSteps(b, moved, positions)
}

func cardPositions(b BoardView) map[string]int {
	positions := make(map[string]int, b.Count())
	for _, column := range b.Columns {
		for _, card := range column.Cards {
			positions[card.ID] = column.Column.Position
		}
	}
	return positions
}

func pullSteps(b BoardView, moved Card, positions map[string]int) ([]PullStep, error) {
	queue := []Card{moved}
	seen := map[string]bool{moved.ID: true}
	plan := make([]PullStep, 0)

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		minRequired := positions[current.ID]
		for _, dependent := range Dependents(current, b.All()) {
			if seen[dependent.ID] {
				continue
			}
			if positions[dependent.ID] >= minRequired {
				continue // already at or after the required stage
			}

			if target, ok := b.ColumnAt(minRequired); !ok {
				continue
			} else if blocked := Unfinished(dependent, minRequired, viewWithStaged(b, positions)); len(blocked) > 0 {
				return nil, fmt.Errorf("%w: %s cannot move with this card: %s (%s) is too far behind",
					ErrWouldLeaveDependents, strconv.Quote(dependent.Title),
					titles(blocked), WhereIn(b, blocked[0].ID))
			} else {
				plan = append(plan, PullStep{CardID: dependent.ID, TargetColumn: target})
				positions[dependent.ID] = target.Position
				seen[dependent.ID] = true
				dependent.ColumnID = target.ID
				queue = append(queue, dependent)
			}
		}
	}
	return plan, nil
}

func viewWithStaged(b BoardView, positions map[string]int) BoardView {
	staged := make(map[int][]Card, len(b.Columns))
	for _, column := range b.Columns {
		for _, card := range column.Cards {
			position := column.Column.Position
			if moved, ok := positions[card.ID]; ok {
				position = moved
			}
			staged[position] = append(staged[position], card)
		}
	}

	view := BoardView{Columns: make([]ColumnView, len(b.Columns))}
	for i, column := range b.Columns {
		view.Columns[i] = ColumnView{Column: column.Column, Cards: staged[column.Column.Position]}
	}
	return view
}

func titles(cards []Card) string {
	names := make([]string, 0, len(cards))
	for _, c := range cards {
		names = append(names, strconv.Quote(c.Title))
	}
	return strings.Join(names, ", ")
}

// WhereIn returns where the card sits — "still in X" for its column, or a
// fallback when the card is not on the board. Error messages use it to point
// at the state that caused the refusal.
func WhereIn(b BoardView, cardID string) string {
	position := b.Position(cardID)
	if column, ok := b.ColumnAt(position); ok && position >= 0 {
		return fmt.Sprintf("still in %q", column.Name)
	}
	return "is no longer on the board"
}
