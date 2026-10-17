package usecase

import (
	"context"
	"sort"

	"kanban-app/internal/domain"
	"kanban-app/internal/ports"
)

// boardReader is the shared read model: every service asks it for one board so
// the columns, ordering and orphan handling stay identical.
type boardReader struct {
	repo ports.BoardRepository
}

// get returns one board's columns in order, each with its cards.
func (r boardReader) get(ctx context.Context, boardID string) (domain.BoardView, error) {
	columns, err := r.repo.GetColumns(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}
	cards, err := r.repo.GetAll(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}
	return assembleBoard(columns, cards), nil
}

// assembleBoard groups the stored cards under their columns. A card pointing
// at a column that no longer exists (a hand-edited file, a deletion that did
// not finish) is shown in the first column rather than disappearing.
func assembleBoard(columns []domain.Column, cards []domain.Card) domain.BoardView {
	columns = domain.SortColumns(columns)

	byColumn := make(map[string][]domain.Card, len(columns))
	for _, c := range cards {
		byColumn[c.ColumnID] = append(byColumn[c.ColumnID], c)
	}

	view := domain.BoardView{Columns: make([]domain.ColumnView, 0, len(columns))}
	for _, column := range columns {
		view.Columns = append(view.Columns, domain.ColumnView{Column: column, Cards: byColumn[column.ID]})
		delete(byColumn, column.ID)
	}
	attachOrphans(&view, byColumn)
	return view
}

// attachOrphans appends cards left without a column to the first column.
func attachOrphans(view *domain.BoardView, leftovers map[string][]domain.Card) {
	if len(leftovers) == 0 || len(view.Columns) == 0 {
		return
	}
	orphans := make([]domain.Card, 0)
	for _, cards := range leftovers {
		orphans = append(orphans, cards...)
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].ID < orphans[j].ID })
	view.Columns[0].Cards = append(view.Columns[0].Cards, orphans...)
}
