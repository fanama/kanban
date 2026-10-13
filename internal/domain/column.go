package domain

import (
	"sort"
	"strings"
)

// Column is a stage of the board. Cards advance through the columns in position
// order; the columns are editable at runtime (created and deleted by the user).
type Column struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

// DefaultColumns is what a brand new board starts with. The IDs match the status
// names used before the columns became editable, so a database written by an
// earlier version keeps its cards where they were.
func DefaultColumns() []Column {
	return []Column{
		{ID: "To Do", Name: "To Do", Position: 0},
		{ID: "In Progress", Name: "In Progress", Position: 1},
		{ID: "Done", Name: "Done", Position: 2},
	}
}

// SortColumns returns the columns in position order, renumbering them from zero
// so a storage that returns gaps or duplicates still gives a usable board.
func SortColumns(columns []Column) []Column {
	sorted := append([]Column(nil), columns...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Position != sorted[j].Position {
			return sorted[i].Position < sorted[j].Position
		}
		return sorted[i].ID < sorted[j].ID
	})
	for i := range sorted {
		sorted[i].Position = i
	}
	return sorted
}

// CleanColumnName trims the name a user typed.
func CleanColumnName(name string) string {
	return strings.TrimSpace(name)
}
