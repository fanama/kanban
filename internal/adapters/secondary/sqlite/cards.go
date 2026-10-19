package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"kanban-app/internal/domain"
)

const selectColumns = `id, title, description, objective, status, priority, version`

func (r *Repo) GetAll(ctx context.Context, boardID string) ([]domain.Card, error) {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+selectColumns+` FROM cards WHERE board_id = ? ORDER BY position, rowid`, boardID)
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
	dependencies, err := r.loadDependencies(ctx, boardID)
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
	if c.Version <= 0 {
		c.Version = 1
	}
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		return insertCardTx(ctx, tx, boardID, c)
	})
	switch {
	case errors.Is(err, domain.ErrCardExists), errors.Is(err, domain.ErrUnknownDependency):
		return domain.Card{}, err
	case err != nil:
		return domain.Card{}, fmt.Errorf("sqlite: create card %s: %w", c.ID, err)
	}
	return c, nil
}

func insertCardTx(ctx context.Context, tx *sql.Tx, boardID string, c domain.Card) error {
	if c.Version <= 0 {
		c.Version = 1
	}
	position, err := nextPositionTx(ctx, tx, boardID, c.ColumnID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO cards (id, title, description, objective, status, priority, position, version, board_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Title, c.Description, c.Objective, c.ColumnID, string(c.Priority), position, c.Version, boardID); err != nil {
		return err
	}
	return insertDependencies(ctx, tx, c.ID, c.DependsOn)
}

func (r *Repo) Update(ctx context.Context, boardID string, c domain.Card) error {
	if err := r.requireBoard(ctx, boardID); err != nil {
		return err
	}
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		return updateCardTx(ctx, tx, boardID, c)
	})
	switch {
	case errors.Is(err, domain.ErrCardNotFound), errors.Is(err, domain.ErrCardConflict), errors.Is(err, domain.ErrUnknownDependency):
		return err
	case err != nil:
		return fmt.Errorf("sqlite: update card %s: %w", c.ID, err)
	}
	return nil
}

func updateCardTx(ctx context.Context, tx *sql.Tx, boardID string, c domain.Card) error {
	current, currentColumn, position, err := currentState(ctx, tx, boardID, c.ID)
	if err != nil {
		return err
	}
	if c.Version > 0 && current != c.Version {
		return domain.ErrCardConflict
	}
	if currentColumn != c.ColumnID {
		if position, err = nextPositionTx(ctx, tx, boardID, c.ColumnID); err != nil {
			return err
		}
	}
	version := current + 1
	res, err := tx.ExecContext(ctx,
		`UPDATE cards
		    SET title = ?, description = ?, objective = ?, status = ?, priority = ?, position = ?, version = ?
		  WHERE id = ? AND board_id = ?`,
		c.Title, c.Description, c.Objective, c.ColumnID, string(c.Priority), position, version, c.ID, boardID)
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
}

func currentState(ctx context.Context, tx *sql.Tx, boardID, cardID string) (int, string, int, error) {
	var version, position int
	var columnID string
	err := tx.QueryRowContext(ctx,
		`SELECT version, status, position FROM cards WHERE id = ? AND board_id = ?`,
		cardID, boardID).Scan(&version, &columnID, &position)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, "", 0, domain.ErrCardNotFound
	case err != nil:
		return 0, "", 0, err
	}
	return version, columnID, position, nil
}

func (r *Repo) UpdateColumn(ctx context.Context, boardID, cardID, columnID string) error {
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx,
			`SELECT status FROM cards WHERE id = ? AND board_id = ?`, cardID, boardID).Scan(&current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrCardNotFound
			}
			return err
		}
		if current == columnID {
			return nil
		}
		position, err := nextPositionTx(ctx, tx, boardID, columnID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE cards SET status = ?, position = ?, version = version + 1
			  WHERE id = ? AND board_id = ?`, columnID, position, cardID, boardID)
		return err
	})
	if err != nil {
		return fmt.Errorf("sqlite: move card %s: %w", cardID, err)
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

func (r *Repo) loadDependencies(ctx context.Context, boardID string) (map[string][]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT d.card_id, d.depends_on_id
		   FROM card_dependencies d
		   JOIN cards c ON c.id = d.card_id
		  WHERE c.board_id = ?
		  ORDER BY d.card_id, d.position`, boardID)
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
