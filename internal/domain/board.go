package domain

// Board is one kanban board: a named workspace holding its own columns and
// cards. A storage adapter may keep several; the default is created with the
// first board start.
type Board struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
