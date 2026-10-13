// Package sqlite is a secondary adapter: it stores the boards in a SQLite file.
// The application core never imports this package; only cmd/server does.
//
// The driver (github.com/mattn/go-sqlite3) is cgo-based, matching the other Go
// service in this repository (dnd/backend).
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	sqlite3 "github.com/mattn/go-sqlite3"

	"kanban-app/internal/domain"
	"kanban-app/internal/ports"
)

// The cards table keeps the historical column name "status" for its column ID:
// the default column IDs are the status names used before the columns became
// editable, so a database written by an earlier version needs no data rewrite.
//
// Cards and columns are scoped to a board; the boards table gives the workspaces
// their names. A database written before boards existed has all its rows slotted
// into the default board by migrate.
const schema = `
CREATE TABLE IF NOT EXISTS boards (
	id       TEXT PRIMARY KEY,
	name     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS cards (
	id          TEXT PRIMARY KEY,
	title       TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	objective   TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL,
	priority    TEXT NOT NULL,
	created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	board_id    TEXT NOT NULL DEFAULT 'default'
);
CREATE INDEX IF NOT EXISTS idx_cards_status ON cards(status);

CREATE TABLE IF NOT EXISTS card_dependencies (
	card_id       TEXT NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
	depends_on_id TEXT NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
	position      INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (card_id, depends_on_id)
);

CREATE TABLE IF NOT EXISTS columns (
	id       TEXT PRIMARY KEY,
	name     TEXT NOT NULL,
	position INTEGER NOT NULL,
	board_id TEXT NOT NULL DEFAULT 'default'
);
`

// defaultBoardID is where a database written before boards existed puts its
// content. cmd/server seeds the same ID on an empty storage, and the seeded
// boards keep the migration invisible.
const defaultBoardID = "default"

// defaultBoardName is the name the default board row carries on a legacy file.
const defaultBoardName = "My board"

// addedColumns lists the card columns introduced after the first release.
var addedColumns = []struct{ name, ddl string }{
	{"description", `ALTER TABLE cards ADD COLUMN description TEXT NOT NULL DEFAULT ''`},
	{"objective", `ALTER TABLE cards ADD COLUMN objective TEXT NOT NULL DEFAULT ''`},
}

// Repo is the SQLite-backed BoardRepository.
type Repo struct {
	db *sql.DB
}

var _ ports.BoardRepository = (*Repo)(nil)

// Open opens (creating it if needed) the database at path and applies the schema.
func Open(ctx context.Context, path string) (*Repo, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite: database path is required")
	}

	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %s: %w", path, err)
	}

	// SQLite accepts one writer at a time; a single connection keeps the app
	// free of "database is locked" errors without any retry logic.
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: ping %s: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: apply schema: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	return &Repo{db: db}, nil
}

// migrate brings an existing database up to the current schema.
func migrate(ctx context.Context, db *sql.DB) error {
	cardColumns, err := tableColumns(ctx, db, "cards")
	if err != nil {
		return err
	}
	columnColumns, err := tableColumns(ctx, db, "columns")
	if err != nil {
		return err
	}

	// Columns introduced after the first release. Databases created by an
	// older version get them here; the former free-text "constraints" column
	// is no longer read or written, and is simply left in place.
	for _, col := range addedColumns {
		if cardColumns[col.name] {
			continue
		}
		if _, err := db.ExecContext(ctx, col.ddl); err != nil {
			return fmt.Errorf("sqlite: add column %s: %w", col.name, err)
		}
	}

	// Cards and columns written before boards existed all belong to the
	// default board. The board indexing happens here rather than in the schema
	// statement: a legacy file must get its board_id column back first.
	legacy := false
	for _, mc := range []struct {
		table   string
		existing map[string]bool
	}{
		{"cards", cardColumns},
		{"columns", columnColumns},
	} {
		if mc.existing["board_id"] {
			continue
		}
		legacy = true
		if _, err := db.ExecContext(ctx, fmt.Sprintf(
			`ALTER TABLE %s ADD COLUMN board_id TEXT NOT NULL DEFAULT '%s'`, mc.table, defaultBoardID)); err != nil {
			return fmt.Errorf("sqlite: add board_id to %s: %w", mc.table, err)
		}
	}
	for _, table := range []string{"cards", "columns"} {
		if _, err := db.ExecContext(ctx,
			fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_board ON %s(board_id)`, table, table)); err != nil {
			return fmt.Errorf("sqlite: index %s.board_id: %w", table, err)
		}
	}
	// A file that predates the boards gets its default board row, so the
	// workspaces list shows its columns and cards. A fresh database gets
	// nothing: cmd/server seeds the board for it.
	if legacy {
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO boards (id, name) VALUES (?, ?)`, defaultBoardID, defaultBoardName); err != nil {
			return fmt.Errorf("sqlite: seed the default board: %w", err)
		}
	}
	return nil
}

func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: read table info of %s: %w", table, err)
	}
	defer rows.Close()

	names := map[string]bool{}
	for rows.Next() {
		var (
			cid, notNull, pk int
			name, colType    string
			defaultValue     sql.NullString
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return nil, fmt.Errorf("sqlite: scan table info of %s: %w", table, err)
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: read table info of %s: %w", table, err)
	}
	return names, nil
}

// Close releases the database handle.
func (r *Repo) Close() error { return r.db.Close() }

// --- boards -----------------------------------------------------------------

func (r *Repo) ListBoards(ctx context.Context) ([]domain.Board, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name FROM boards ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: read boards: %w", err)
	}
	defer rows.Close()

	boards := make([]domain.Board, 0)
	for rows.Next() {
		var board domain.Board
		if err := rows.Scan(&board.ID, &board.Name); err != nil {
			return nil, fmt.Errorf("sqlite: scan board: %w", err)
		}
		boards = append(boards, board)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: read boards: %w", err)
	}
	return boards, nil
}

func (r *Repo) CreateBoard(ctx context.Context, board domain.Board) (domain.Board, error) {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO boards (id, name) VALUES (?, ?)`, board.ID, board.Name)

	var serr sqlite3.Error
	if errors.As(err, &serr) && serr.Code == sqlite3.ErrConstraint {
		return domain.Board{}, domain.ErrBoardExists
	}
	if err != nil {
		return domain.Board{}, fmt.Errorf("sqlite: create board %s: %w", board.ID, err)
	}
	return board, nil
}// DeleteBoard drops the workspace and everything it holds in one transaction:
// either the board vanishes entirely or it is untouched. The foreign keys take
// the dependency rows with the cards. An unknown board is an error.
func (r *Repo) DeleteBoard(ctx context.Context, id string) error {
	if err := r.requireBoard(ctx, id); err != nil {
		return err
	}
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		// The dependency rows and the cards go together through the foreign
		// keys; the columns and the workspace row are explicit.
		if _, err := tx.ExecContext(ctx, `DELETE FROM cards WHERE board_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM columns WHERE board_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM boards WHERE id = ?`, id); err != nil {
			return err
		}
		return nil
	})
	switch {
	case errors.Is(err, domain.ErrCardExists), errors.Is(err, domain.ErrUnknownDependency):
		return err
	case err != nil:
		return fmt.Errorf("sqlite: delete board %s: %w", id, err)
	}
	return nil
}

// boardExists reports whether the board row is there, so a storage-backed
// method can answer an unknown board the same way an unknown column is answered.
func (r *Repo) boardExists(ctx context.Context, boardID string) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx, `SELECT 1 FROM boards WHERE id = ?`, boardID).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("sqlite: read board %s: %w", boardID, err)
	}
	return true, nil
}

// requireBoard maps an unknown board to ErrBoardNotFound, like it is done at the
// use case boundary.
func (r *Repo) requireBoard(ctx context.Context, boardID string) error {
	ok, err := r.boardExists(ctx, boardID)
	if err != nil || ok {
		return err
	}
	return domain.ErrBoardNotFound
}

// --- cards ------------------------------------------------------------------

const selectColumns = `id, title, description, objective, status, priority`

func (r *Repo) GetAll(ctx context.Context, boardID string) ([]domain.Card, error) {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+selectColumns+` FROM cards WHERE board_id = ? ORDER BY rowid`, boardID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: read cards: %w", err)
	}
	defer rows.Close()

	cards := make([]domain.Card, 0)
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		cards = append(cards, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: read cards: %w", err)
	}

	dependencies, err := r.loadDependencies(ctx)
	if err != nil {
		return nil, err
	}
	for i := range cards {
		cards[i].DependsOn = dependencies[cards[i].ID]
	}
	return cards, nil
}

func (r *Repo) GetByID(ctx context.Context, boardID, id string) (domain.Card, error) {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return domain.Card{}, err
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT `+selectColumns+` FROM cards WHERE id = ? AND board_id = ?`, id, boardID)

	c, err := scanCard(row)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return domain.Card{}, domain.ErrCardNotFound
	case err != nil:
		return domain.Card{}, err
	}

	deps, err := r.dependenciesOf(ctx, id)
	if err != nil {
		return domain.Card{}, err
	}
	c.DependsOn = deps
	return c, nil
}

func (r *Repo) Create(ctx context.Context, boardID string, c domain.Card) (domain.Card, error) {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return domain.Card{}, err
	}
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO cards (id, title, description, objective, status, priority, board_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			c.ID, c.Title, c.Description, c.Objective, c.ColumnID, string(c.Priority), boardID); err != nil {
			return err
		}
		return insertDependencies(ctx, tx, c.ID, c.DependsOn)
	})
	switch {
	case errors.Is(err, domain.ErrCardExists), errors.Is(err, domain.ErrUnknownDependency):
		return domain.Card{}, err
	case err != nil:
		return domain.Card{}, fmt.Errorf("sqlite: create card %s: %w", c.ID, err)
	}
	return c, nil
}

func (r *Repo) Update(ctx context.Context, boardID string, c domain.Card) error {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return err
	}
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE cards
			    SET title = ?, description = ?, objective = ?, status = ?, priority = ?
			  WHERE id = ? AND board_id = ?`,
			c.Title, c.Description, c.Objective, c.ColumnID, string(c.Priority), c.ID, boardID)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return domain.ErrCardNotFound
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM card_dependencies WHERE card_id = ?`, c.ID); err != nil {
			return err
		}
		return insertDependencies(ctx, tx, c.ID, c.DependsOn)
	})
	switch {
	case errors.Is(err, domain.ErrCardNotFound), errors.Is(err, domain.ErrUnknownDependency):
		return err
	case err != nil:
		return fmt.Errorf("sqlite: update card %s: %w", c.ID, err)
	}
	return nil
}

func (r *Repo) UpdateColumn(ctx context.Context, boardID, cardID, columnID string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE cards SET status = ? WHERE id = ? AND board_id = ?`, columnID, cardID, boardID)
	if err != nil {
		return fmt.Errorf("sqlite: move card %s: %w", cardID, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: move card %s: %w", cardID, err)
	}
	if affected == 0 {
		return domain.ErrCardNotFound
	}
	return nil
}

// DeleteCard removes the card's row; the foreign keys take both directions of
// the dependency graph with it, so the cards that pointed at it simply stop
// keeping it as a constraint.
func (r *Repo) DeleteCard(ctx context.Context, boardID, cardID string) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM cards WHERE id = ? AND board_id = ?`, cardID, boardID)
	if err != nil {
		return fmt.Errorf("sqlite: delete card %s: %w", cardID, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: delete card %s: %w", cardID, err)
	}
	if affected == 0 {
		return domain.ErrCardNotFound
	}
	return nil
}

// --- columns ----------------------------------------------------------------

func (r *Repo) GetColumns(ctx context.Context, boardID string) ([]domain.Column, error) {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, position FROM columns WHERE board_id = ? ORDER BY position`, boardID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: read columns: %w", err)
	}
	defer rows.Close()

	columns := make([]domain.Column, 0)
	for rows.Next() {
		var column domain.Column
		if err := rows.Scan(&column.ID, &column.Name, &column.Position); err != nil {
			return nil, fmt.Errorf("sqlite: scan column: %w", err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: read columns: %w", err)
	}
	return domain.SortColumns(columns), nil
}

func (r *Repo) CreateColumn(ctx context.Context, boardID string, column domain.Column) (domain.Column, error) {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return domain.Column{}, err
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO columns (id, name, position, board_id) VALUES (?, ?, ?, ?)`,
		column.ID, column.Name, column.Position, boardID)

	var serr sqlite3.Error
	if errors.As(err, &serr) && serr.Code == sqlite3.ErrConstraint {
		return domain.Column{}, domain.ErrColumnExists
	}
	if err != nil {
		return domain.Column{}, fmt.Errorf("sqlite: create column %s: %w", column.ID, err)
	}
	return column, nil
}

// RenameColumn replaces a column's name in place.
func (r *Repo) RenameColumn(ctx context.Context, boardID, id, name string) error {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE columns SET name = ? WHERE id = ? AND board_id = ?`, name, id, boardID)
	if err != nil {
		return fmt.Errorf("sqlite: rename column %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return domain.ErrColumnNotFound
	}
	return nil
}

func (r *Repo) DeleteColumn(ctx context.Context, boardID, id string) error {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return err
	}
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		// Read the position first: once the row is gone the gap can no longer
		// be located, and the surviving columns must keep a dense ordering.
		var position int
		err := tx.QueryRowContext(ctx,
			`SELECT position FROM columns WHERE id = ? AND board_id = ?`, id, boardID).Scan(&position)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return domain.ErrColumnNotFound
		case err != nil:
			return err
		}

		if _, err := tx.ExecContext(ctx,
			`DELETE FROM columns WHERE id = ? AND board_id = ?`, id, boardID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE columns SET position = position - 1 WHERE position > ? AND board_id = ?`, position, boardID)
		return err
	})
	switch {
	case errors.Is(err, domain.ErrColumnNotFound):
		return err
	case err != nil:
		return fmt.Errorf("sqlite: delete column %s: %w", id, err)
	}
	return nil
}

// --- import -----------------------------------------------------------------

// ReplaceBoard wipes the content of the board and writes the snapshot in, in
// one transaction: either the whole file lands or nothing changes. The cards
// are inserted before their constraint rows so the foreign keys always see the
// targets they point at.
func (r *Repo) ReplaceBoard(ctx context.Context, boardID string, columns []domain.Column, cards []domain.Card) error {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return err
	}
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM card_dependencies WHERE card_id IN (SELECT id FROM cards WHERE board_id = ?)`, boardID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM cards WHERE board_id = ?`, boardID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM columns WHERE board_id = ?`, boardID); err != nil {
			return err
		}
		for _, column := range domain.SortColumns(columns) {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO columns (id, name, position, board_id) VALUES (?, ?, ?, ?)`,
				column.ID, column.Name, column.Position, boardID); err != nil {
				return err
			}
		}
		for _, card := range cards {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO cards (id, title, description, objective, status, priority, board_id)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				card.ID, card.Title, card.Description, card.Objective, card.ColumnID, string(card.Priority), boardID); err != nil {
				return err
			}
		}
		for _, card := range cards {
			if err := insertDependencies(ctx, tx, card.ID, card.DependsOn); err != nil {
				return err
			}
		}
		return nil
	})
	switch {
	case errors.Is(err, domain.ErrUnknownDependency), errors.Is(err, domain.ErrCardExists):
		return err
	case err != nil:
		return fmt.Errorf("sqlite: replace board %s: %w", boardID, err)
	}
	return nil
}

// inTx runs fn in a transaction and maps driver constraint failures to domain
// errors: a duplicate primary key means the card exists, a broken foreign key
// means a constraint card was not found.
func (r *Repo) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return translate(err)
	}
	return translate(tx.Commit())
}

func translate(err error) error {
	var serr sqlite3.Error
	if !errors.As(err, &serr) || serr.Code != sqlite3.ErrConstraint {
		return err
	}
	if strings.Contains(serr.Error(), "FOREIGN KEY") {
		return domain.ErrUnknownDependency
	}
	return domain.ErrCardExists
}

func insertDependencies(ctx context.Context, tx *sql.Tx, cardID string, deps []string) error {
	for position, dep := range deps {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO card_dependencies (card_id, depends_on_id, position) VALUES (?, ?, ?)`,
			cardID, dep, position); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) loadDependencies(ctx context.Context) (map[string][]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT card_id, depends_on_id FROM card_dependencies ORDER BY card_id, position`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: read dependencies: %w", err)
	}
	defer rows.Close()

	dependencies := map[string][]string{}
	for rows.Next() {
		var cardID, dependsOn string
		if err := rows.Scan(&cardID, &dependsOn); err != nil {
			return nil, fmt.Errorf("sqlite: scan dependency: %w", err)
		}
		dependencies[cardID] = append(dependencies[cardID], dependsOn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: read dependencies: %w", err)
	}
	return dependencies, nil
}

func (r *Repo) dependenciesOf(ctx context.Context, cardID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT depends_on_id FROM card_dependencies WHERE card_id = ? ORDER BY position`, cardID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: read dependencies of %s: %w", cardID, err)
	}
	defer rows.Close()

	var deps []string
	for rows.Next() {
		var dependsOn string
		if err := rows.Scan(&dependsOn); err != nil {
			return nil, fmt.Errorf("sqlite: scan dependency: %w", err)
		}
		deps = append(deps, dependsOn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: read dependencies of %s: %w", cardID, err)
	}
	return deps, nil
}

type scanner interface{ Scan(dest ...any) error }

func scanCard(s scanner) (domain.Card, error) {
	var id, title, description, objective, columnID, priority string
	if err := s.Scan(&id, &title, &description, &objective, &columnID, &priority); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Card{}, err
		}
		return domain.Card{}, fmt.Errorf("sqlite: scan card: %w", err)
	}
	return domain.Card{
		ID:          id,
		Title:       title,
		Description: description,
		Objective:   objective,
		ColumnID:    columnID,
		Priority:    domain.Priority(priority).Normalize(),
	}, nil
}
