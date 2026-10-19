package sqlite

import (
	"context"
	"database/sql"
	"fmt"
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
	position    INTEGER NOT NULL DEFAULT 0,
	version     INTEGER NOT NULL DEFAULT 1,
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
	id       TEXT NOT NULL,
	name     TEXT NOT NULL,
	position INTEGER NOT NULL,
	board_id TEXT NOT NULL DEFAULT 'default',
	PRIMARY KEY (board_id, id)
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
	{"version", `ALTER TABLE cards ADD COLUMN version INTEGER NOT NULL DEFAULT 1`},
	{"position", `ALTER TABLE cards ADD COLUMN position INTEGER NOT NULL DEFAULT 0`},
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

	if err := addCardColumns(ctx, db, cardColumns); err != nil {
		return err
	}
	legacy, err := addBoardScoping(ctx, db, cardColumns, columnColumns)
	if err != nil {
		return err
	}
	if err := scopeColumnKeys(ctx, db); err != nil {
		return err
	}
	if err := indexBoardColumns(ctx, db); err != nil {
		return err
	}
	if legacy {
		return seedDefaultBoard(ctx, db)
	}
	return nil
}

// addCardColumns adds the card columns introduced after the first release.
// The former free-text "constraints" column is no longer read or written and
// is simply left in place.
func addCardColumns(ctx context.Context, db *sql.DB, existing map[string]bool) error {
	for _, col := range addedColumns {
		if existing[col.name] {
			continue
		}
		if _, err := db.ExecContext(ctx, col.ddl); err != nil {
			return fmt.Errorf("sqlite: add column %s: %w", col.name, err)
		}
	}
	return nil
}

// addBoardScoping adds board_id to tables written before boards existed and
// reports whether the file was legacy.
func addBoardScoping(ctx context.Context, db *sql.DB, cardColumns, columnColumns map[string]bool) (bool, error) {
	legacy := false
	for _, target := range []struct {
		table    string
		existing map[string]bool
	}{
		{"cards", cardColumns},
		{"columns", columnColumns},
	} {
		if target.existing["board_id"] {
			continue
		}
		legacy = true
		if _, err := db.ExecContext(ctx, fmt.Sprintf(
			`ALTER TABLE %s ADD COLUMN board_id TEXT NOT NULL DEFAULT '%s'`, target.table, defaultBoardID)); err != nil {
			return false, fmt.Errorf("sqlite: add board_id to %s: %w", target.table, err)
		}
	}
	return legacy, nil
}

// scopeColumnKeys rebuilds a columns table written before a column's ID was
// scoped to its board. That table keyed the ID globally, so a second board
// could not hold its own "To Do"; the board_id prefix makes the pair unique.
func scopeColumnKeys(ctx context.Context, db *sql.DB) error {
	scoped, err := columnKeysAreScoped(ctx, db)
	if err != nil || scoped {
		return err
	}
	err = rebuildColumnsTable(ctx, db)
	if err != nil {
		return fmt.Errorf("sqlite: scope column keys to the board: %w", err)
	}
	return nil
}

// columnKeysAreScoped reports whether the primary key of columns is
// (board_id, id) rather than the legacy id alone.
func columnKeysAreScoped(ctx context.Context, db *sql.DB) (bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(columns)`)
	if err != nil {
		return false, fmt.Errorf("sqlite: read table info of columns: %w", err)
	}
	defer rows.Close()

	pk := map[string]int{}
	for rows.Next() {
		var (
			cid, notNull, pkPosition int
			name, colType            string
			defaultValue             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pkPosition); err != nil {
			return false, fmt.Errorf("sqlite: scan table info of columns: %w", err)
		}
		pk[name] = pkPosition
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("sqlite: read table info of columns: %w", err)
	}
	return pk["board_id"] > 0 && pk["id"] > 0, nil
}

// rebuildColumnsTable copies the rows into a table with the composite key and
// swaps it in; a failure leaves the original table untouched.
func rebuildColumnsTable(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	ddl := []string{
		`CREATE TABLE columns_scoped (
			id       TEXT NOT NULL,
			name     TEXT NOT NULL,
			position INTEGER NOT NULL,
			board_id TEXT NOT NULL DEFAULT 'default',
			PRIMARY KEY (board_id, id)
		)`,
		`INSERT INTO columns_scoped (id, name, position, board_id)
		 SELECT id, name, position, board_id FROM columns`,
		`DROP TABLE columns`,
		`ALTER TABLE columns_scoped RENAME TO columns`,
	}
	for _, statement := range ddl {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func indexBoardColumns(ctx context.Context, db *sql.DB) error {
	for _, table := range []string{"cards", "columns"} {
		if _, err := db.ExecContext(ctx,
			fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_board ON %s(board_id)`, table, table)); err != nil {
			return fmt.Errorf("sqlite: index %s.board_id: %w", table, err)
		}
	}
	return nil
}

// seedDefaultBoard gives a legacy file its default board row, so the
// workspaces list shows its columns and cards. A fresh database gets nothing:
// cmd/server seeds the board for it.
func seedDefaultBoard(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO boards (id, name) VALUES (?, ?)`, defaultBoardID, defaultBoardName); err != nil {
		return fmt.Errorf("sqlite: seed the default board: %w", err)
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
