package usecase

import (
	"context"
	"fmt"
	"strings"

	"kanban-app/internal/domain"
	"kanban-app/internal/events"
	"kanban-app/internal/ports"
)

// transferService implements ports.ImportExportService.
type transferService struct {
	repo   ports.BoardRepository
	reader boardReader
	pub    events.Publisher
}

// ExportBoard returns one board as a snapshot meant to travel: the columns in
// order and every card.
func (s *transferService) ExportBoard(ctx context.Context, boardID string) (domain.BoardSnapshot, error) {
	board, err := s.reader.get(ctx, boardID)
	if err != nil {
		return domain.BoardSnapshot{}, err
	}
	return domain.BoardSnapshot{Columns: board.OrderedColumns(), Cards: board.All()}, nil
}

// ImportBoard replaces one board's content with a snapshot from an export
// file. The same rules the live board obeys are applied: a title is mandatory,
// a constraint must exist and cannot be the card itself or form a cycle. A card
// whose column does not exist in the file falls back to the first column.
func (s *transferService) ImportBoard(ctx context.Context, boardID string, snapshot domain.BoardSnapshot) (ports.BoardChange, error) {
	before, err := s.reader.get(ctx, boardID)
	if err != nil {
		return ports.BoardChange{}, err
	}
	columns, err := importColumns(snapshot.Columns)
	if err != nil {
		return ports.BoardChange{}, err
	}
	cards, err := importCards(snapshot.Cards, columns)
	if err != nil {
		return ports.BoardChange{}, err
	}
	for i := range cards {
		if cards[i].Version < 1 {
			cards[i].Version = 1
		}
	}
	if err := s.repo.ReplaceBoard(ctx, boardID, columns, cards); err != nil {
		return ports.BoardChange{}, err
	}
	after, err := s.reader.get(ctx, boardID)
	if err != nil {
		return ports.BoardChange{}, err
	}
	notice := events.Notice{Kind: events.BoardImported, BoardID: boardID}
	change := changeFor(ctx, events.BoardImported, boardID, before, after, notice)
	change.Reload = true
	publishChange(ctx, s.pub, change)
	return change, nil
}

// importCards normalizes, places and validates the cards of an import file.
func importCards(raw []domain.Card, columns []domain.Column) ([]domain.Card, error) {
	cards, byID, err := normalizeImportCards(raw)
	if err != nil {
		return nil, err
	}
	applyColumnFallback(cards, byID, columns)
	if err := validateImportCards(cards, byID); err != nil {
		return nil, err
	}
	return cards, nil
}

func normalizeImportCards(raw []domain.Card) ([]domain.Card, map[string]domain.Card, error) {
	cards := make([]domain.Card, 0, len(raw))
	byID := make(map[string]domain.Card, len(raw))
	for _, rawCard := range raw {
		card := rawCard
		card.ID = strings.TrimSpace(card.ID)
		card.Title = strings.TrimSpace(card.Title)
		if card.ID == "" {
			return nil, nil, fmt.Errorf("%w: card %q has no ID", domain.ErrInvalidImport, card.Title)
		}
		if card.Title == "" {
			return nil, nil, domain.ErrEmptyTitle
		}
		card.Priority = card.Priority.Normalize()
		card.DependsOn = cleanIDs(card.DependsOn)
		if _, duplicate := byID[card.ID]; duplicate {
			return nil, nil, fmt.Errorf("%w: duplicate card ID %s", domain.ErrInvalidImport, card.ID)
		}
		byID[card.ID] = card
		cards = append(cards, card)
	}
	return cards, byID, nil
}

// applyColumnFallback points cards with an unknown column at the first one.
func applyColumnFallback(cards []domain.Card, byID map[string]domain.Card, columns []domain.Column) {
	firstColumn := columns[0].ID
	for i := range cards {
		if columnExists(columns, cards[i].ColumnID) {
			continue
		}
		cards[i].ColumnID = firstColumn
		byID[cards[i].ID] = cards[i]
	}
}

// validateImportCards checks that constraints exist, are not the card itself
// and do not create a cycle.
func validateImportCards(cards []domain.Card, byID map[string]domain.Card) error {
	for _, card := range cards {
		for _, dep := range card.DependsOn {
			if _, ok := byID[dep]; !ok {
				return fmt.Errorf("%w: %s", domain.ErrUnknownDependency, dep)
			}
			if dep == card.ID {
				return fmt.Errorf("%w: %s", domain.ErrSelfDependency, card.Title)
			}
		}
		if createsCycle(card, byID) {
			return fmt.Errorf("%w: %s", domain.ErrDependencyCycle, card.Title)
		}
	}
	return nil
}

// importColumns checks the columns of an import file: a non-empty name, no
// duplicated ID, and positions renumbered densely just like the board keeps
// them.
func importColumns(raw []domain.Column) ([]domain.Column, error) {
	columns := make([]domain.Column, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for i, column := range domain.SortColumns(raw) {
		column.Name = domain.CleanColumnName(column.Name)
		if column.Name == "" {
			return nil, domain.ErrEmptyColumnName
		}
		if seen[column.ID] {
			return nil, fmt.Errorf("%w: duplicate column ID %s", domain.ErrInvalidImport, column.ID)
		}
		seen[column.ID] = true
		column.Position = i
		columns = append(columns, column)
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("%w: the file has no column", domain.ErrInvalidImport)
	}
	return columns, nil
}

func columnExists(columns []domain.Column, id string) bool {
	for _, column := range columns {
		if column.ID == id {
			return true
		}
	}
	return false
}
