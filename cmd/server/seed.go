package main

import (
	"context"
	"fmt"

	"kanban-app/internal/domain"
	"kanban-app/internal/ports"
)

// defaultBoardID is the workspace a fresh storage starts with.
const defaultBoardID = "default"

// seed makes sure a fresh storage holds a board, its columns and a couple of
// demo cards. It only touches empty storage, so an existing board is never
// modified.
func seed(ctx context.Context, repo ports.BoardRepository) error {
	board, err := ensureBoard(ctx, repo)
	if err != nil {
		return err
	}
	columns, err := ensureColumns(ctx, repo, board)
	if err != nil {
		return err
	}
	return ensureCards(ctx, repo, board, columns)
}

func ensureBoard(ctx context.Context, repo ports.BoardRepository) (domain.Board, error) {
	boards, err := repo.ListBoards(ctx)
	if err != nil {
		return domain.Board{}, err
	}
	if len(boards) > 0 {
		return boards[0], nil
	}

	board, err := repo.CreateBoard(ctx, domain.Board{ID: defaultBoardID, Name: "My board"})
	if err != nil {
		return domain.Board{}, err
	}
	fmt.Println("Created the default board " + board.Name + ".")
	return board, nil
}

func ensureColumns(ctx context.Context, repo ports.BoardRepository, board domain.Board) ([]domain.Column, error) {
	columns, err := repo.GetColumns(ctx, board.ID)
	if err != nil {
		return nil, err
	}
	if len(columns) > 0 {
		return columns, nil
	}

	for _, column := range domain.DefaultColumns() {
		if _, err := repo.CreateColumn(ctx, board.ID, column); err != nil {
			return nil, err
		}
	}
	fmt.Println("Created the default columns: To Do, In Progress, Done.")
	return domain.DefaultColumns(), nil
}

func ensureCards(ctx context.Context, repo ports.BoardRepository, board domain.Board, columns []domain.Column) error {
	cards, err := repo.GetAll(ctx, board.ID)
	if err != nil {
		return err
	}
	if len(cards) > 0 {
		return nil
	}

	first := domain.SortColumns(columns)[0]
	for _, card := range demoCards(first.ID) {
		if _, err := repo.Create(ctx, board.ID, card); err != nil {
			return err
		}
	}
	fmt.Printf("Seeded the empty board with 2 demo cards in %q.\n", first.Name)
	return nil
}

func demoCards(columnID string) []domain.Card {
	return []domain.Card{
		{ID: "card_1", Title: "Clean Architecture Design", ColumnID: columnID, Priority: domain.PriorityHigh},
		{ID: "card_2", Title: "Wire HTMX Frontend", ColumnID: columnID, Priority: domain.PriorityMedium},
	}
}
