package gsheets

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"kanban-app/internal/domain"
)

// DeleteBoard refuses: a spreadsheet holds exactly one board; bring another
// one if you want more.
func (r *Repo) DeleteBoard(ctx context.Context, id string) error {
	_ = ctx
	_ = id
	return domain.ErrBoardsUnsupported
}

// CreateBoard refuses: a spreadsheet holds exactly one board; the tab you
// pointed the storage at is it.
func (r *Repo) CreateBoard(ctx context.Context, board domain.Board) (domain.Board, error) {
	_ = ctx
	_ = board
	return domain.Board{}, domain.ErrBoardsUnsupported
}

// ListBoards reports the one board the tab holds. The board keeps the tab title
// as ID (stable: it is what the range notation already quotes) and as name —
// which is what a human reading the tab calls it too.
func (r *Repo) ListBoards(ctx context.Context) ([]domain.Board, error) {
	_ = ctx
	name := strings.Trim(r.sheet, "'")
	return []domain.Board{{ID: name, Name: name}}, nil
}

// cardFromRow maps a sheet row to a domain card. Rows written by hand may leave
// the ID column empty, in which case the row number becomes the ID. Missing
// trailing cells (a sheet still using the 4-column layout) read as empty, and a
// card whose column is unknown is placed by the use case's GetBoard fallback.
func cardFromRow(row []any, index int) domain.Card {
	return domain.Card{
		ID:          cardID(row, index),
		Title:       cell(row, 1),
		Description: cell(row, 4),
		Objective:   cell(row, 5),
		DependsOn:   parseIDs(cell(row, 6)),
		ColumnID:    cell(row, 2),
		Priority:    domain.Priority(cell(row, 3)).Normalize(),
		Version:     cardVersion(row),
	}
}

// cardVersion reads the optional H column. Sheets written before versioning
// existed have no cell there and read as version 1.
func cardVersion(row []any) int {
	version, err := strconv.Atoi(cell(row, 7))
	if err != nil || version < 1 {
		return 1
	}
	return version
}

func cardID(row []any, index int) string {
	if id := cell(row, 0); id != "" {
		return id
	}
	if cell(row, 1) == "" && cell(row, 2) == "" && cell(row, 3) == "" {
		return "" // fully blank row
	}
	return fmt.Sprintf("row_%d", index+2)
}

func cell(row []any, i int) string {
	if i >= len(row) {
		return ""
	}
	if s, ok := row[i].(string); ok {
		return strings.TrimSpace(s)
	}
	return fmt.Sprintf("%v", row[i])
}
