package domain

import "errors"

var (
	ErrCardNotFound = errors.New("card not found")
	ErrEmptyTitle   = errors.New("card title must not be empty")
	ErrCardExists   = errors.New("card already exists")

	// ErrCardBlocked is returned when a card cannot move forward because a
	// constraint is not far enough along the board.
	ErrCardBlocked = errors.New("card is blocked by a constraint")

	// ErrWouldLeaveDependents is returned when a constraint card cannot move
	// forward because a card that depends on it can not be pulled along: a
	// constraint never passes the cards waiting for it.
	ErrWouldLeaveDependents = errors.New("a card that depends on this one would be left behind")

	ErrUnknownDependency = errors.New("constraint card does not exist")
	ErrSelfDependency    = errors.New("a card cannot be its own constraint")
	ErrDependencyCycle   = errors.New("constraints would form a cycle")

	// ErrInvalidImport is returned when a board file cannot be loaded: the
	// details (a missing column, a duplicate card ID…) name what is wrong.
	ErrInvalidImport = errors.New("invalid board file")

	ErrBoardNotFound    = errors.New("board not found")
	ErrEmptyBoardName   = errors.New("board name must not be empty")
	ErrBoardExists      = errors.New("a board with this name already exists")
	ErrLastBoard        = errors.New("there must always be at least one board")
	ErrBoardsUnsupported = errors.New("this storage keeps a single board")

	ErrColumnNotFound  = errors.New("column not found")
	ErrEmptyColumnName = errors.New("column name must not be empty")
	ErrColumnExists    = errors.New("column already exists")
	ErrLastColumn      = errors.New("the board must keep at least one column")
)
