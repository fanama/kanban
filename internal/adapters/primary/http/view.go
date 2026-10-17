package http

import (
	"errors"
	"net/http"

	"kanban-app/internal/domain"
)

// cardView is a card plus what the UI needs about its neighbours: the stage it
// may go back to, the one it may advance to, and what holds it there. Blockers
// are the constraints that are not far enough; Teamed are the cards that depend
// on this one and have not caught up.
type cardView struct {
	domain.Card
	Prev     *domain.Column
	Next     *domain.Column
	Blockers []domain.Card
	Teamed   []domain.Card
}

// columnView is one column of the board, ready to render.
type columnView struct {
	Column    domain.Column
	Cards     []cardView
	CanDelete bool
}

// boardView is one board page: the sidebar (with its sibling boards) and the
// ordered list of columns with their cards.
type boardView struct {
	BoardID   string
	Board     domain.Board
	Boards    []domain.Board
	Columns   []columnView
	CardCount int
}

// formView is the card being edited, the cards it may depend on, and the stages
// it may be placed in.
type formView struct {
	domain.Card
	Candidates []domain.Card
	Columns    []domain.Column
}

// Selected reports whether a candidate is already a constraint of the card.
func (f formView) Selected(id string) bool { return f.DependsOnCard(id) }

// ColumnName resolves a column ID to its name, for the candidate list.
func (f formView) ColumnName(id string) string {
	for _, column := range f.Columns {
		if column.ID == id {
			return column.Name
		}
	}
	return id
}

// toView decorates every card with the stage rules that apply to it: the
// constraints that hold it back, the dependents that hold it down, and the
// neighbours it may move to. The boards bar rides along.
func toView(board domain.BoardView, boards []domain.Board, boardID string) boardView {
	view := boardView{
		BoardID:   boardID,
		Boards:    boards,
		Columns:   make([]columnView, 0, len(board.Columns)),
		CardCount: board.Count(),
	}
	for _, candidate := range boards {
		if candidate.ID == boardID {
			view.Board = candidate
		}
	}

	canDelete := len(board.Columns) > 1
	for index := range board.Columns {
		view.Columns = append(view.Columns, decorateColumn(board, index, canDelete))
	}
	return view
}

func decorateColumn(board domain.BoardView, index int, canDelete bool) columnView {
	view := columnView{Column: board.Columns[index].Column, CanDelete: canDelete}
	for _, card := range board.Columns[index].Cards {
		view.Cards = append(view.Cards, decorateCard(board, index, card))
	}
	return view
}

func decorateCard(board domain.BoardView, index int, card domain.Card) cardView {
	view := cardView{Card: card}

	if index > 0 {
		previous := board.Columns[index-1].Column
		view.Prev = &previous
	}
	if index+1 < len(board.Columns) {
		next := board.Columns[index+1].Column
		view.Next = &next
		view.Blockers = domain.Unfinished(card, next.Position, board)
		// The dependents are normally pulled along; only a pull impossible by
		// the stage rules leaves someone waiting, so plan the pull to know
		// whether the button must be locked.
		if _, err := domain.PullPlan(card, next.Position, board); err != nil {
			view.Teamed = domain.AheadOfDependents(card, next.Position, board)
		}
	}
	return view
}

// cardViewOf renders a single card with the same decoration as the board.
func cardViewOf(card domain.Card, board domain.BoardView) cardView {
	for index, column := range board.Columns {
		for _, candidate := range column.Cards {
			if candidate.ID == card.ID {
				return decorateCard(board, index, card)
			}
		}
	}
	return cardView{Card: card}
}

// candidates lists the cards a card may depend on: all the others.
func candidates(card domain.Card, all []domain.Card) []domain.Card {
	others := make([]domain.Card, 0, len(all))
	for _, candidate := range all {
		if candidate.ID != card.ID {
			others = append(others, candidate)
		}
	}
	return others
}

// statusFor maps a domain error to the HTTP status the client should see.
func statusFor(err error) int {
	switch {
	case errors.Is(err, domain.ErrCardNotFound), errors.Is(err, domain.ErrColumnNotFound), errors.Is(err, domain.ErrBoardNotFound):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrEmptyTitle),
		errors.Is(err, domain.ErrCardBlocked),
		errors.Is(err, domain.ErrWouldLeaveDependents),
		errors.Is(err, domain.ErrUnknownDependency),
		errors.Is(err, domain.ErrSelfDependency),
		errors.Is(err, domain.ErrDependencyCycle),
		errors.Is(err, domain.ErrEmptyColumnName),
		errors.Is(err, domain.ErrColumnExists),
		errors.Is(err, domain.ErrLastColumn),
		errors.Is(err, domain.ErrInvalidImport),
		errors.Is(err, domain.ErrEmptyBoardName),
		errors.Is(err, domain.ErrBoardExists),
		errors.Is(err, domain.ErrLastBoard),
		errors.Is(err, domain.ErrBoardsUnsupported):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
