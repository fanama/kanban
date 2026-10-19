package http

import (
	"fmt"
	"testing"

	"kanban-app/internal/domain"
)

func BenchmarkToView200Cards(b *testing.B) {
	board := benchmarkBoard(10, 20)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if view := toView(board, nil, "bench"); view.CardCount != 200 {
			b.Fatalf("unexpected card count %d", view.CardCount)
		}
	}
}

func benchmarkBoard(columnCount, cardsPerColumn int) domain.BoardView {
	board := domain.BoardView{Columns: make([]domain.ColumnView, columnCount)}
	for column := 0; column < columnCount; column++ {
		id := fmt.Sprintf("col_%d", column)
		board.Columns[column] = domain.ColumnView{
			Column: domain.Column{ID: id, Name: id, Position: column},
			Cards:  benchmarkCards(column, cardsPerColumn),
		}
	}
	return board
}

func benchmarkCards(column, count int) []domain.Card {
	cards := make([]domain.Card, 0, count)
	for i := 0; i < count; i++ {
		card := domain.Card{
			ID:       fmt.Sprintf("card_%d_%d", column, i),
			Title:    fmt.Sprintf("Card %d/%d", column, i),
			ColumnID: fmt.Sprintf("col_%d", column),
			Priority: domain.PriorityMedium,
			Version:  1,
		}
		if column > 0 {
			card.DependsOn = []string{fmt.Sprintf("card_%d_%d", column-1, i)}
		}
		cards = append(cards, card)
	}
	return cards
}
