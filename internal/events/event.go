// Package events holds the application-level event bus used by the observer
// pattern: use cases publish a board change, the HTTP adapter subscribes and
// fans it out to WebSocket clients.
package events

import (
	"context"
	"strings"
	"time"
	"unicode"
)

// Kind names one observable board change.
type Kind string

const (
	BoardCreated  Kind = "board.created"
	BoardDeleted  Kind = "board.deleted"
	BoardImported Kind = "board.imported"

	ColumnAdded   Kind = "column.added"
	ColumnRenamed Kind = "column.renamed"
	ColumnDeleted Kind = "column.deleted"

	CardAdded   Kind = "card.added"
	CardUpdated Kind = "card.updated"
	CardMoved   Kind = "card.moved"
	CardDeleted Kind = "card.deleted"
)

// Notice carries the structured, already-safe facts a browser needs to build
// a toast. It never contains HTML.
type Notice struct {
	Kind       Kind   `json:"kind"`
	Actor      string `json:"actor,omitempty"`
	BoardID    string `json:"board_id,omitempty"`
	BoardName  string `json:"board_name,omitempty"`
	CardID     string `json:"card_id,omitempty"`
	CardTitle  string `json:"card_title,omitempty"`
	ColumnID   string `json:"column_id,omitempty"`
	ColumnName string `json:"column_name,omitempty"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
}

// Event is one immutable change notification. Seq is assigned by the bus per
// board; board-list events keep Seq zero.
type Event struct {
	Seq            uint64
	Kind           Kind
	BoardID        string
	BoardName      string
	Actor          string
	ClientID       string
	CardIDs        []string
	ColumnIDs      []string
	RemovedCards   []string
	RemovedColumns []string
	Reload         bool
	Notice         Notice
	At             time.Time
}

// Publisher is the narrow seam the use cases depend on.
type Publisher interface {
	Publish(Event)
}

// NopPublisher is used by read-only tests and callers that do not care about
// live updates.
type NopPublisher struct{}

func (NopPublisher) Publish(Event) {}

type actorKey struct{}
type clientKey struct{}

// WithActor stores the self-declared display name of the request author.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, CleanActor(actor))
}

// WithClientID stores the browser tab identifier used for echo suppression.
func WithClientID(ctx context.Context, clientID string) context.Context {
	return context.WithValue(ctx, clientKey{}, cleanToken(clientID))
}

// Actor returns the sanitized display name, or "Someone".
func Actor(ctx context.Context) string {
	if actor, ok := ctx.Value(actorKey{}).(string); ok && actor != "" {
		return actor
	}
	return "Someone"
}

// ClientID returns the browser tab identifier, if one was supplied.
func ClientID(ctx context.Context) string {
	if clientID, ok := ctx.Value(clientKey{}).(string); ok {
		return clientID
	}
	return ""
}

// CleanActor trims and bounds a display name. It is a label, never an
// authorization identity.
func CleanActor(actor string) string {
	actor = strings.Join(strings.Fields(actor), " ")
	actor = strings.TrimFunc(actor, func(r rune) bool { return unicode.IsControl(r) })
	if actor == "" {
		return "Someone"
	}
	runes := []rune(actor)
	if len(runes) > 40 {
		actor = string(runes[:40])
	}
	return actor
}

func cleanToken(token string) string {
	token = strings.TrimSpace(token)
	token = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, token)
	if len(token) > 80 {
		token = token[:80]
	}
	return token
}
