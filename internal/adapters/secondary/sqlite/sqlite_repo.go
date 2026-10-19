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

	if err := prepareDatabase(ctx, db, path); err != nil {
		db.Close()
		return nil, err
	}
	return &Repo{db: db}, nil
}

func prepareDatabase(ctx context.Context, db *sql.DB, path string) error {
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("sqlite: ping %s: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("sqlite: apply schema: %w", err)
	}
	return migrate(ctx, db)
}

// Close releases the database handle.
func (r *Repo) Close() error { return r.db.Close() }

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

type scanner interface{ Scan(dest ...any) error }

func scanCard(s scanner) (domain.Card, error) {
	var id, title, description, objective, columnID, priority string
	var version int
	if err := s.Scan(&id, &title, &description, &objective, &columnID, &priority, &version); err != nil {
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
		Version:     version,
	}, nil
}
