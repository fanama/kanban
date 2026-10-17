// Package memory is a secondary adapter: an in-process implementation of the
// BoardRepository output port. Used for tests and local development.
//
// The store holds several boards. Card and column IDs are checked across all
// boards (like the SQLite adapter's primary keys), so an ID never points at two
// different cards depending on which board you look from.
package memory

import (
	"context"
	"sort"
	"sync"

	"kanban-app/internal/domain"
	"kanban-app/internal/ports"
)

// board is what one board's storage state is made of. Cards are kept in the
// repo's global map; the board carries the board-scoped parts.
type memoryBoard struct {
	columns []domain.Column
	cards   map[string]bool // IDs of the cards that belong to this board
}

type memoryRepo struct {
	mu     sync.RWMutex
	boards []domain.Board
	byID   map[string]*memoryBoard
	cards  map[string]domain.Card // global: one ID never means two cards
}

var _ ports.BoardRepository = (*memoryRepo)(nil)

// MemoryBoard seeds one board in a repo built for tests and demos.
type MemoryBoard struct {
	ID      string
	Name    string
	Columns []domain.Column
	Cards   []domain.Card
}

// NewMemoryRepo builds a repository holding the given boards, or an empty one
// without any (the caller then creates boards through the use case).
func NewMemoryRepo(boards ...MemoryBoard) ports.BoardRepository {
	m := &memoryRepo{
		byID:  make(map[string]*memoryBoard, len(boards)),
		cards: make(map[string]domain.Card),
	}
	for _, seed := range boards {
		id := seed.ID
		if id == "" {
			id = seed.Name
		}
		board := &memoryBoard{
			columns: domain.SortColumns(seed.Columns),
			cards:   make(map[string]bool, len(seed.Cards)),
		}
		for _, c := range seed.Cards {
			m.cards[c.ID] = clone(c)
			board.cards[c.ID] = true
		}
		m.boards = append(m.boards, domain.Board{ID: id, Name: seed.Name})
		m.byID[id] = board
	}
	sort.Slice(m.boards, func(i, j int) bool { return m.boards[i].ID < m.boards[j].ID })
	return m
}

// clone copies a card, including its dependency slice, so callers can never
// mutate what is in the store through an alias.
func clone(c domain.Card) domain.Card {
	if c.DependsOn != nil {
		c.DependsOn = append([]string(nil), c.DependsOn...)
	}
	return c
}

// board returns the state of a known board.
func (r *memoryRepo) state(boardID string) (*memoryBoard, bool) {
	board, ok := r.byID[boardID]
	return board, ok
}

func (r *memoryRepo) ListBoards(ctx context.Context) ([]domain.Board, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]domain.Board, len(r.boards))
	copy(result, r.boards)
	return result, nil
}

func (r *memoryRepo) CreateBoard(ctx context.Context, board domain.Board) (domain.Board, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.byID[board.ID]; exists {
		return domain.Board{}, domain.ErrBoardExists
	}
	for _, existing := range r.boards {
		if existing.Name == board.Name {
			return domain.Board{}, domain.ErrBoardExists
		}
	}
	r.boards = append(r.boards, board)
	sort.Slice(r.boards, func(i, j int) bool { return r.boards[i].ID < r.boards[j].ID })
	r.byID[board.ID] = &memoryBoard{columns: nil, cards: make(map[string]bool)}
	return board, nil
}

func (r *memoryRepo) DeleteBoard(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	board, ok := r.byID[id]
	if !ok {
		return domain.ErrBoardNotFound
	}
	// Its cards go with it; a constraint pointing at one of them is gone too.
	for cardID := range board.cards {
		delete(r.cards, cardID)
	}
	kept := r.boards[:0]
	for _, b := range r.boards {
		if b.ID != id {
			kept = append(kept, b)
		}
	}
	r.boards = kept
	delete(r.byID, id)
	return nil
}

func (r *memoryRepo) GetAll(ctx context.Context, boardID string) ([]domain.Card, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	board, ok := r.state(boardID)
	if !ok {
		return nil, domain.ErrBoardNotFound
	}

	result := make([]domain.Card, 0, len(board.cards))
	for id := range board.cards {
		result = append(result, clone(r.cards[id]))
	}
	// Map iteration is random; keep the board stable between renders.
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (r *memoryRepo) GetByID(ctx context.Context, boardID, id string) (domain.Card, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	board, ok := r.state(boardID)
	if !ok || !board.cards[id] {
		return domain.Card{}, domain.ErrCardNotFound
	}
	return clone(r.cards[id]), nil
}

func (r *memoryRepo) Create(ctx context.Context, boardID string, card domain.Card) (domain.Card, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	board, ok := r.state(boardID)
	if !ok {
		return domain.Card{}, domain.ErrBoardNotFound
	}
	if _, exists := r.cards[card.ID]; exists {
		return domain.Card{}, domain.ErrCardExists
	}
	board.cards[card.ID] = true
	r.cards[card.ID] = clone(card)
	return clone(card), nil
}

func (r *memoryRepo) Update(ctx context.Context, boardID string, card domain.Card) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	board, ok := r.state(boardID)
	if !ok || !board.cards[card.ID] {
		return domain.ErrCardNotFound
	}
	r.cards[card.ID] = clone(card)
	return nil
}

func (r *memoryRepo) UpdateColumn(ctx context.Context, boardID, cardID, columnID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	board, ok := r.state(boardID)
	if !ok || !board.cards[cardID] {
		return domain.ErrCardNotFound
	}
	c := clone(r.cards[cardID])
	c.ColumnID = columnID
	r.cards[cardID] = c
	return nil
}

// DeleteCard removes the card and drops it from the constraint lists of the
// cards that pointed at it, so nobody is left waiting for a ghost.
func (r *memoryRepo) DeleteCard(ctx context.Context, boardID, cardID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	board, ok := r.state(boardID)
	if !ok || !board.cards[cardID] {
		return domain.ErrCardNotFound
	}
	delete(r.cards, cardID)
	delete(board.cards, cardID)
	for id, card := range r.cards {
		if card.DependsOnCard(cardID) {
			kept := make([]string, 0, len(card.DependsOn))
			for _, dep := range card.DependsOn {
				if dep != cardID {
					kept = append(kept, dep)
				}
			}
			card.DependsOn = kept
			r.cards[id] = clone(card)
		}
	}
	return nil
}

func (r *memoryRepo) GetColumns(ctx context.Context, boardID string) ([]domain.Column, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	board, ok := r.state(boardID)
	if !ok {
		return nil, domain.ErrBoardNotFound
	}
	return append([]domain.Column(nil), board.columns...), nil
}

func (r *memoryRepo) CreateColumn(ctx context.Context, boardID string, column domain.Column) (domain.Column, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	board, ok := r.state(boardID)
	if !ok {
		return domain.Column{}, domain.ErrBoardNotFound
	}
	for _, existing := range board.columns {
		if existing.ID == column.ID {
			return domain.Column{}, domain.ErrColumnExists
		}
	}
	// The position is the caller's: appending is the use case's decision.
	board.columns = domain.SortColumns(append(board.columns, column))
	return column, nil
}

func (r *memoryRepo) RenameColumn(ctx context.Context, boardID, id, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	board, ok := r.state(boardID)
	if !ok {
		return domain.ErrBoardNotFound
	}
	for i := range board.columns {
		if board.columns[i].ID == id {
			board.columns[i].Name = name
			return nil
		}
	}
	return domain.ErrColumnNotFound
}

func (r *memoryRepo) DeleteColumn(ctx context.Context, boardID, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	board, ok := r.state(boardID)
	if !ok {
		return domain.ErrBoardNotFound
	}

	position := -1
	kept := make([]domain.Column, 0, len(board.columns))
	for _, column := range board.columns {
		if column.ID == id {
			position = column.Position
			continue
		}
		kept = append(kept, column)
	}
	if position < 0 {
		return domain.ErrColumnNotFound
	}

	board.columns = domain.SortColumns(kept)
	return nil
}

// ReplaceBoard swaps the whole content of the board in one go: the import path
// has already validated the snapshot, whatever storage it came from.
func (r *memoryRepo) ReplaceBoard(ctx context.Context, boardID string, columns []domain.Column, cards []domain.Card) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	board, ok := r.state(boardID)
	if !ok {
		return domain.ErrBoardNotFound
	}
	for cardID := range board.cards {
		delete(r.cards, cardID)
	}
	board.columns = domain.SortColumns(append([]domain.Column(nil), columns...))
	board.cards = make(map[string]bool, len(cards))
	for _, c := range cards {
		r.cards[c.ID] = clone(c)
		board.cards[c.ID] = true
	}
	return nil
}
