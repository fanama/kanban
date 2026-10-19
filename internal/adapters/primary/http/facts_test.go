package http

import (
	"testing"

	"kanban-app/internal/domain"
)

type pullCase struct {
	name   string
	card   domain.Card
	target int
	cards  []domain.Card
}

func TestCanPullMatchesDomainPullPlan(t *testing.T) {
	for _, tc := range pullCases() {
		t.Run(tc.name, func(t *testing.T) {
			board := boardOf(tc.cards)
			facts := newBoardFacts(board)
			got := facts.canPull(tc.card, tc.target)
			_, err := domain.PullPlan(tc.card, tc.target, board)
			if got != (err == nil) {
				t.Fatalf("canPull = %v, PullPlan error = %v", got, err)
			}
		})
	}
}

func pullCases() []pullCase {
	return []pullCase{
		{
			name: "no dependent", target: 2,
			card:  domain.Card{ID: "c", ColumnID: "c1"},
			cards: []domain.Card{{ID: "c", ColumnID: "c1"}},
		},
		{
			name: "dependent is pulled", target: 2,
			card: domain.Card{ID: "c", ColumnID: "c1"},
			cards: []domain.Card{
				{ID: "c", ColumnID: "c1"},
				{ID: "d", ColumnID: "c0", DependsOn: []string{"c"}},
			},
		},
		{
			name: "dependent is blocked", target: 2,
			card: domain.Card{ID: "c", ColumnID: "c1"},
			cards: []domain.Card{
				{ID: "c", ColumnID: "c1"},
				{ID: "d", ColumnID: "c1", DependsOn: []string{"c", "e"}},
				{ID: "e", ColumnID: "c0"},
			},
		},
	}
}

func boardOf(cards []domain.Card) domain.BoardView {
	board := domain.BoardView{Columns: []domain.ColumnView{
		{Column: domain.Column{ID: "c0", Name: "c0", Position: 0}},
		{Column: domain.Column{ID: "c1", Name: "c1", Position: 1}},
		{Column: domain.Column{ID: "c2", Name: "c2", Position: 2}},
	}}
	for _, card := range cards {
		for i := range board.Columns {
			if board.Columns[i].Column.ID == card.ColumnID {
				board.Columns[i].Cards = append(board.Columns[i].Cards, card)
			}
		}
	}
	return board
}
