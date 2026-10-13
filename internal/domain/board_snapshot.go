package domain

// BoardSnapshot is the whole board in a form meant to travel: the columns in
// position order and every card. It is what the JSON export writes and the JSON
// import reads, so its shape is a contract between a saved file and any board —
// the person who loads it may even be using a different storage adapter.
type BoardSnapshot struct {
	Columns []Column `json:"columns"`
	Cards   []Card   `json:"cards"`
}
