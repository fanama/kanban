// Package usecase implements the application layer: it orchestrates the domain
// and the output ports, and knows nothing about any concrete adapter.
package usecase

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"kanban-app/internal/domain"
	"kanban-app/internal/ports"
)

type boardUseCase struct {
	repo ports.BoardRepository

	// sequence feeds generated IDs. Adapters that own IDs (e.g. Sheets row
	// numbers) are re-read on every GetBoard, so this is only a hint.
	sequence atomic.Int64
}

// NewBoardUseCase wires the application core to a repository.
func NewBoardUseCase(repo ports.BoardRepository) ports.BoardUseCase {
	uc := &boardUseCase{repo: repo}
	uc.sequence.Store(time.Now().UnixNano())
	return uc
}

// --- boards -----------------------------------------------------------------

// ListBoards returns the boards the storage holds.
func (uc *boardUseCase) ListBoards(ctx context.Context) ([]domain.Board, error) {
	return uc.repo.ListBoards(ctx)
}

// CreateBoard appends a board: a new workspace with its own columns and cards.
func (uc *boardUseCase) CreateBoard(ctx context.Context, name string) (domain.Board, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.Board{}, domain.ErrEmptyBoardName
	}

	boards, err := uc.repo.ListBoards(ctx)
	if err != nil {
		return domain.Board{}, err
	}
	for _, existing := range boards {
		if strings.EqualFold(existing.Name, name) {
			return domain.Board{}, fmt.Errorf("%w: %s", domain.ErrBoardExists, name)
		}
	}

	board := domain.Board{ID: uc.nextID("bd"), Name: name}
	if _, err := uc.repo.CreateBoard(ctx, board); err != nil {
		return domain.Board{}, err
	}
	// A new board comes ready to use: the default stages To Do / In Progress /
	// Done are created with it.
	for _, column := range domain.DefaultColumns() {
		if _, err := uc.repo.CreateColumn(ctx, board.ID, column); err != nil {
			return domain.Board{}, err
		}
	}
	return board, nil
}

// DeleteBoard removes a board and everything on it — columns and cards alike.
// The last board stays: a blank state is an import (or a fresh storage) away.
func (uc *boardUseCase) DeleteBoard(ctx context.Context, id string) error {
	boards, err := uc.repo.ListBoards(ctx)
	if err != nil {
		return err
	}
	if len(boards) <= 1 {
		return domain.ErrLastBoard
	}
	return uc.repo.DeleteBoard(ctx, id)
}

// --- reading ----------------------------------------------------------------

// GetBoard returns one board's columns in order, each with its cards.
func (uc *boardUseCase) GetBoard(ctx context.Context, boardID string) (domain.BoardView, error) {
	columns, err := uc.repo.GetColumns(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}
	columns = domain.SortColumns(columns)

	cards, err := uc.repo.GetAll(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}

	byColumn := make(map[string][]domain.Card, len(columns))
	for _, c := range cards {
		byColumn[c.ColumnID] = append(byColumn[c.ColumnID], c)
	}

	view := domain.BoardView{Columns: make([]domain.ColumnView, 0, len(columns))}
	for _, column := range columns {
		view.Columns = append(view.Columns, domain.ColumnView{Column: column, Cards: byColumn[column.ID]})
		delete(byColumn, column.ID)
	}

	// A card pointing at a column that no longer exists (a hand-edited file, a
	// deletion that did not finish) is shown in the first column rather than
	// disappearing.
	if len(byColumn) > 0 {
		orphans := make([]domain.Card, 0)
		for _, cards := range byColumn {
			orphans = append(orphans, cards...)
		}
		sort.Slice(orphans, func(i, j int) bool { return orphans[i].ID < orphans[j].ID })
		if len(view.Columns) > 0 {
			view.Columns[0].Cards = append(view.Columns[0].Cards, orphans...)
		}
	}

	return view, nil
}

// --- cards ------------------------------------------------------------------

// AddCard appends a new card to the first column of the board.
func (uc *boardUseCase) AddCard(ctx context.Context, boardID, title string, priority domain.Priority) (domain.BoardView, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return domain.BoardView{}, domain.ErrEmptyTitle
	}

	board, err := uc.GetBoard(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}
	first, ok := board.ColumnAt(0)
	if !ok {
		return domain.BoardView{}, fmt.Errorf("%w: the board has no column to add cards to", domain.ErrColumnNotFound)
	}

	card := domain.Card{
		ID:       uc.nextID("card"),
		Title:    title,
		ColumnID: first.ID,
		Priority: priority.Normalize(),
	}

	if _, err := uc.repo.Create(ctx, boardID, card); err != nil {
		return domain.BoardView{}, err
	}

	return uc.GetBoard(ctx, boardID)
}

// DeleteCard removes a card. Cards that depend on it keep their other
// constraints: the deleted one is dropped from their lists, so nobody keeps
// waiting for a card that is no longer there.
func (uc *boardUseCase) DeleteCard(ctx context.Context, boardID, cardID string) (domain.BoardView, error) {
	board, err := uc.GetBoard(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}

	card, ok := board.Index()[cardID]
	if !ok {
		return domain.BoardView{}, domain.ErrCardNotFound
	}

	for _, dependent := range domain.Dependents(card, board.All()) {
		kept := make([]string, 0, len(dependent.DependsOn))
		for _, dep := range dependent.DependsOn {
			if dep != cardID {
				kept = append(kept, dep)
			}
		}
		updated := dependent
		updated.DependsOn = kept
		if err := uc.repo.Update(ctx, boardID, updated); err != nil {
			return domain.BoardView{}, err
		}
	}

	if err := uc.repo.DeleteCard(ctx, boardID, cardID); err != nil {
		return domain.BoardView{}, err
	}
	return uc.GetBoard(ctx, boardID)
}

// MoveCard moves a card to a column, enforcing the stage rule. Going forward,
// the cards that depend on it are pulled along (a constraint never passes its
// dependents); going backward, those same cards are demoted with it.
func (uc *boardUseCase) MoveCard(ctx context.Context, boardID, cardID, columnID string) (domain.BoardView, error) {
	board, err := uc.GetBoard(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}

	card, ok := board.Index()[cardID]
	if !ok {
		return domain.BoardView{}, domain.ErrCardNotFound
	}
	target, ok := board.Column(columnID)
	if !ok {
		return domain.BoardView{}, fmt.Errorf("%w: %s", domain.ErrColumnNotFound, columnID)
	}

	from := board.Position(card.ID)
	if from == target.Column.Position {
		return board, nil
	}

	if target.Column.Position > from {
		if err := checkStage(card, target.Column, board); err != nil {
			return domain.BoardView{}, err
		}
	}

	if err := uc.repo.UpdateColumn(ctx, boardID, card.ID, target.Column.ID); err != nil {
		return domain.BoardView{}, err
	}

	if target.Column.Position < from {
		if err := uc.demote(ctx, boardID, board, card.ID, target.Column.Position); err != nil {
			return domain.BoardView{}, err
		}
	}
	if target.Column.Position > from {
		if err := uc.pullDependents(ctx, boardID, board, card.ID, target.Column.Position); err != nil {
			return domain.BoardView{}, err
		}
	}

	return uc.GetBoard(ctx, boardID)
}

// UpdateCard saves an edited card. Text fields left empty by the caller keep
// their stored value. A nil constraint list also keeps the stored constraints
// (a partial edit must not drop them); a non-nil list — even an empty one —
// replaces them, so sending [] removes every constraint.
func (uc *boardUseCase) UpdateCard(ctx context.Context, boardID string, edit domain.Card) (domain.BoardView, error) {
	board, err := uc.GetBoard(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}

	before, ok := board.Index()[edit.ID]
	if !ok {
		return domain.BoardView{}, domain.ErrCardNotFound
	}
	from := board.Position(before.ID)

	updated, err := merge(edit, before)
	if err != nil {
		return domain.BoardView{}, err
	}
	if err := validateDependencies(board.All(), updated); err != nil {
		return domain.BoardView{}, err
	}

	to, ok := board.Column(updated.ColumnID)
	if !ok {
		return domain.BoardView{}, fmt.Errorf("%w: %s", domain.ErrColumnNotFound, updated.ColumnID)
	}
	if to.Column.Position > from {
		if err := checkStage(updated, to.Column, board); err != nil {
			return domain.BoardView{}, err
		}
	}

	if err := uc.repo.Update(ctx, boardID, updated); err != nil {
		return domain.BoardView{}, err
	}

	if to.Column.Position < from {
		if err := uc.demote(ctx, boardID, board, updated.ID, to.Column.Position); err != nil {
			return domain.BoardView{}, err
		}
	}
	if to.Column.Position > from {
		if err := uc.pullDependents(ctx, boardID, board, updated.ID, to.Column.Position); err != nil {
			return domain.BoardView{}, err
		}
	}

	return uc.GetBoard(ctx, boardID)
}

// --- columns ----------------------------------------------------------------

// AddColumn appends a column at the end of the board.
func (uc *boardUseCase) AddColumn(ctx context.Context, boardID, name string) (domain.BoardView, error) {
	name = domain.CleanColumnName(name)
	if name == "" {
		return domain.BoardView{}, domain.ErrEmptyColumnName
	}

	board, err := uc.GetBoard(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}
	for _, column := range board.Columns {
		if strings.EqualFold(column.Column.Name, name) {
			return domain.BoardView{}, fmt.Errorf("%w: %s", domain.ErrColumnExists, name)
		}
	}

	column := domain.Column{
		ID:       uc.nextID("col"),
		Name:     name,
		Position: len(board.Columns),
	}
	if _, err := uc.repo.CreateColumn(ctx, boardID, column); err != nil {
		return domain.BoardView{}, err
	}

	return uc.GetBoard(ctx, boardID)
}

// RenameColumn replaces a column's name: the column keeps its position and
// its cards, so nothing else on the board changes.
func (uc *boardUseCase) RenameColumn(ctx context.Context, boardID, columnID, name string) (domain.BoardView, error) {
	name = domain.CleanColumnName(name)
	if name == "" {
		return domain.BoardView{}, domain.ErrEmptyColumnName
	}

	board, err := uc.GetBoard(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}
	if _, ok := board.Column(columnID); !ok {
		return domain.BoardView{}, fmt.Errorf("%w: %s", domain.ErrColumnNotFound, columnID)
	}
	for _, column := range board.Columns {
		if column.Column.ID != columnID && strings.EqualFold(column.Column.Name, name) {
			return domain.BoardView{}, fmt.Errorf("%w: %s", domain.ErrColumnExists, name)
		}
	}

	if err := uc.repo.RenameColumn(ctx, boardID, columnID, name); err != nil {
		return domain.BoardView{}, err
	}

	return uc.GetBoard(ctx, boardID)
}

// DeleteColumn removes a column and keeps its cards: they fall back to the
// previous stage (or to the column that becomes the first one when the deleted
// column is the first). The cards that depend on them follow, as always.
func (uc *boardUseCase) DeleteColumn(ctx context.Context, boardID, columnID string) (domain.BoardView, error) {
	board, err := uc.GetBoard(ctx, boardID)
	if err != nil {
		return domain.BoardView{}, err
	}

	doomed, ok := board.Column(columnID)
	if !ok {
		return domain.BoardView{}, fmt.Errorf("%w: %s", domain.ErrColumnNotFound, columnID)
	}
	if len(board.Columns) < 2 {
		return domain.BoardView{}, domain.ErrLastColumn
	}

	// The column receiving the cards: the previous one, or the one that will
	// become the first when the first column is deleted.
	fallbackPosition := doomed.Column.Position - 1
	if doomed.Column.Position == 0 {
		fallbackPosition = 1
	}
	fallback, ok := board.ColumnAt(fallbackPosition)
	if !ok {
		return domain.BoardView{}, domain.ErrColumnNotFound
	}

	orphans := append([]domain.Card(nil), doomed.Cards...)

	if err := uc.repo.DeleteColumn(ctx, boardID, columnID); err != nil {
		return domain.BoardView{}, err
	}

	for _, card := range orphans {
		if err := uc.repo.UpdateColumn(ctx, boardID, card.ID, fallback.ID); err != nil {
			return domain.BoardView{}, err
		}
	}

	// The surviving columns have been renumbered and the orphans have moved, so
	// each cascade starts from a fresh read of the board.
	for _, card := range orphans {
		after, err := uc.GetBoard(ctx, boardID)
		if err != nil {
			return domain.BoardView{}, err
		}
		position := after.Position(card.ID)
		if position < 0 {
			continue // already gone from the board: nothing to settle
		}
		if err := uc.demote(ctx, boardID, after, card.ID, position); err != nil {
			return domain.BoardView{}, err
		}
	}

	return uc.GetBoard(ctx, boardID)
}

// --- export / import ---------------------------------------------------------

// ExportBoard returns one board as a snapshot meant to travel: the columns in
// order and every card.
func (uc *boardUseCase) ExportBoard(ctx context.Context, boardID string) (domain.BoardSnapshot, error) {
	board, err := uc.GetBoard(ctx, boardID)
	if err != nil {
		return domain.BoardSnapshot{}, err
	}
	return domain.BoardSnapshot{Columns: board.OrderedColumns(), Cards: board.All()}, nil
}

// ImportBoard replaces one board's content with a snapshot from an export
// file. The same rules the live board obeys are applied: a title is mandatory,
// a constraint must exist and cannot be the card itself or form a cycle. A card
// whose column does not exist in the file falls back to the first column, like
// a card shown after a hand-edited file.
func (uc *boardUseCase) ImportBoard(ctx context.Context, boardID string, snapshot domain.BoardSnapshot) (domain.BoardView, error) {
	columns, err := importColumns(snapshot.Columns)
	if err != nil {
		return domain.BoardView{}, err
	}

	cards := make([]domain.Card, 0, len(snapshot.Cards))
	byID := make(map[string]domain.Card, len(snapshot.Cards))
	for _, raw := range snapshot.Cards {
		card := raw
		card.ID = strings.TrimSpace(card.ID)
		card.Title = strings.TrimSpace(card.Title)
		if card.ID == "" {
			return domain.BoardView{}, fmt.Errorf("%w: card %q has no ID", domain.ErrInvalidImport, card.Title)
		}
		if card.Title == "" {
			return domain.BoardView{}, domain.ErrEmptyTitle
		}
		card.Priority = card.Priority.Normalize()
		card.DependsOn = cleanIDs(card.DependsOn)
		if _, duplicate := byID[card.ID]; duplicate {
			return domain.BoardView{}, fmt.Errorf("%w: duplicate card ID %s", domain.ErrInvalidImport, card.ID)
		}
		byID[card.ID] = card
		cards = append(cards, card)
	}

	firstColumn := columns[0].ID
	for i := range cards {
		if !columnExists(columns, cards[i].ColumnID) {
			cards[i].ColumnID = firstColumn
			byID[cards[i].ID] = cards[i]
		}
		card := cards[i]
		for _, dep := range card.DependsOn {
			if _, ok := byID[dep]; !ok {
				return domain.BoardView{}, fmt.Errorf("%w: %s", domain.ErrUnknownDependency, dep)
			}
			if dep == card.ID {
				return domain.BoardView{}, fmt.Errorf("%w: %s", domain.ErrSelfDependency, card.Title)
			}
		}
		if createsCycle(card, byID) {
			return domain.BoardView{}, fmt.Errorf("%w: %s", domain.ErrDependencyCycle, card.Title)
		}
	}

	if err := uc.repo.ReplaceBoard(ctx, boardID, columns, cards); err != nil {
		return domain.BoardView{}, err
	}
	return uc.GetBoard(ctx, boardID)
}

// importColumns checks the columns of an import file: a non-empty name, no
// duplicated ID, and the positions are renumbered densely just like the board
// keeps them.
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

// --- cascades ---------------------------------------------------------------

// demote moves every card that would end up more than one stage ahead of the
// card now sitting at position, transitively, so that a demoted card always
// drags the cards that depend on it down with it.
func (uc *boardUseCase) demote(ctx context.Context, boardID string, board domain.BoardView, cardID string, position int) error {
	positions := make(map[string]int, board.Count())
	for _, column := range board.Columns {
		for _, c := range column.Cards {
			positions[c.ID] = column.Column.Position
		}
	}
	positions[cardID] = position

	all := board.All()
	card, ok := board.Index()[cardID]
	if !ok {
		return domain.ErrCardNotFound
	}
	card.ColumnID = columnIDAt(board, position)

	queue := []domain.Card{card}
	seen := map[string]bool{cardID: true}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		maxAllowed := positions[current.ID] + 1
		for _, dependent := range domain.Dependents(current, all) {
			if seen[dependent.ID] {
				continue
			}
			if positions[dependent.ID] <= maxAllowed {
				continue
			}

			target, ok := board.ColumnAt(maxAllowed)
			if !ok {
				continue
			}
			if err := uc.repo.UpdateColumn(ctx, boardID, dependent.ID, target.ID); err != nil {
				return err
			}

			positions[dependent.ID] = target.Position
			seen[dependent.ID] = true
			dependent.ColumnID = target.ID
			queue = append(queue, dependent)
		}
	}
	return nil
}

func columnIDAt(board domain.BoardView, position int) string {
	if column, ok := board.ColumnAt(position); ok {
		return column.ID
	}
	return ""
}

// pullDependents carries forward every card that would be left behind by the
// card now sitting at position — the mirror of demote. The plan is computed by
// the domain (pure logic) before anything is written: it is refused when a
// dependent cannot be brought along without breaking its own stage rule.
func (uc *boardUseCase) pullDependents(ctx context.Context, boardID string, board domain.BoardView, cardID string, position int) error {
	plan, err := domain.PullPlan(*mustCard(board, cardID), position, board)
	if err != nil {
		return err
	}
	for _, step := range plan {
		if err := uc.repo.UpdateColumn(ctx, boardID, step.CardID, step.TargetColumn.ID); err != nil {
			return err
		}
	}
	return nil
}

func mustCard(board domain.BoardView, cardID string) *domain.Card {
	card, ok := board.Index()[cardID]
	if !ok {
		return &domain.Card{ID: cardID}
	}
	return &card
}

// --- rules ------------------------------------------------------------------

// checkStage enforces "a card can only move to column n+1 when its constraints
// are at stage n or further".
func checkStage(card domain.Card, target domain.Column, board domain.BoardView) error {
	blockers := domain.Unfinished(card, target.Position, board)
	if len(blockers) == 0 {
		return nil
	}

	required := target.Position - 1
	if required < 0 {
		required = 0
	}
	if column, ok := board.ColumnAt(required); ok {
		return fmt.Errorf("%w: to reach %q, %s must be at %q first",
			domain.ErrCardBlocked, target.Name, titles(blockers), column.Name)
	}
	return fmt.Errorf("%w: %s", domain.ErrCardBlocked, titles(blockers))
}

// merge overlays the edited fields on the stored card and enforces the rules:
// a title is mandatory and the column must exist.
func merge(edit, current domain.Card) (domain.Card, error) {
	updated := current
	updated.Title = strings.TrimSpace(edit.Title)
	if updated.Title == "" {
		return domain.Card{}, domain.ErrEmptyTitle
	}

	updated.Description = strings.TrimSpace(edit.Description)
	updated.Objective = strings.TrimSpace(edit.Objective)
	if edit.DependsOn != nil {
		updated.DependsOn = cleanIDs(edit.DependsOn)
	}
	if edit.ColumnID != "" {
		updated.ColumnID = strings.TrimSpace(edit.ColumnID)
	}
	if edit.Priority != "" {
		updated.Priority = edit.Priority.Normalize()
	}

	return updated, nil
}

// cleanIDs trims the constraint list, drops blanks and duplicates, and keeps the
// order the caller used. The result stays non-nil: an empty list means "no
// constraints", which is different from nil ("leave them alone").
func cleanIDs(ids []string) []string {
	cleaned := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		cleaned = append(cleaned, id)
	}
	return cleaned
}

// validateDependencies checks that a card's constraints exist, are not itself
// and do not create a cycle.
func validateDependencies(cards []domain.Card, card domain.Card) error {
	index := make(map[string]domain.Card, len(cards))
	for _, c := range cards {
		index[c.ID] = c
	}

	for _, id := range card.DependsOn {
		if id == card.ID {
			return domain.ErrSelfDependency
		}
		if _, ok := index[id]; !ok {
			return fmt.Errorf("%w: %s", domain.ErrUnknownDependency, id)
		}
	}

	index[card.ID] = card
	if createsCycle(card, index) {
		return domain.ErrDependencyCycle
	}
	return nil
}

// createsCycle walks the constraints of card: coming back to card means the
// graph would no longer be a DAG, so no card could ever be finished.
func createsCycle(card domain.Card, index map[string]domain.Card) bool {
	visited := map[string]bool{}

	var walk func(id string) bool
	walk = func(id string) bool {
		if id == card.ID {
			return true
		}
		if visited[id] {
			return false
		}
		visited[id] = true
		for _, next := range index[id].DependsOn {
			if walk(next) {
				return true
			}
		}
		return false
	}

	for _, dep := range card.DependsOn {
		if walk(dep) {
			return true
		}
	}
	return false
}

func titles(cards []domain.Card) string {
	names := make([]string, 0, len(cards))
	for _, c := range cards {
		names = append(names, fmt.Sprintf("%q", c.Title))
	}
	return strings.Join(names, ", ")
}

func (uc *boardUseCase) nextID(prefix string) string {
	n := uc.sequence.Add(1)
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().Unix(), n)
}
