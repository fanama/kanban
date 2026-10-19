package events

import (
	"sync"
)

const subscriberBuffer = 32

// Bus is the observer subject. Subscribers receive events through buffered
// channels; a slow subscriber is dropped rather than blocking a mutation.
type Bus struct {
	mu      sync.Mutex
	nextID  int
	seq     map[string]uint64
	clients map[int]*subscription
}

type subscription struct {
	boardID string
	all     bool
	ch      chan Event
	once    sync.Once
}

// NewBus returns an empty in-process bus.
func NewBus() *Bus {
	return &Bus{seq: map[string]uint64{}, clients: map[int]*subscription{}}
}

// SubscribeBoard observes the card/column events of one board.
func (b *Bus) SubscribeBoard(boardID string) (<-chan Event, func()) {
	return b.subscribe(boardID, false)
}

// SubscribeBoards observes board create/delete events (the sidebar).
func (b *Bus) SubscribeBoards() (<-chan Event, func()) {
	return b.subscribe("", true)
}

func (b *Bus) subscribe(boardID string, all bool) (<-chan Event, func()) {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	sub := &subscription{boardID: boardID, all: all, ch: make(chan Event, subscriberBuffer)}
	b.clients[id] = sub
	b.mu.Unlock()

	return sub.ch, func() { b.unsubscribe(id) }
}

func (b *Bus) unsubscribe(id int) {
	b.mu.Lock()
	sub, ok := b.clients[id]
	if ok {
		delete(b.clients, id)
	}
	b.mu.Unlock()
	if !ok {
		return
	}
	sub.once.Do(func() { close(sub.ch) })
}

// Publish notifies every matching observer. It never blocks: a full channel
// drops that subscriber, whose browser will reconnect and resync.
func (b *Bus) Publish(event Event) {
	b.mu.Lock()
	if event.BoardID != "" {
		b.seq[event.BoardID]++
		event.Seq = b.seq[event.BoardID]
	}
	targets := make([]*subscription, 0, len(b.clients))
	for id, sub := range b.clients {
		if !sub.matches(event) {
			continue
		}
		select {
		case sub.ch <- event:
		default:
			delete(b.clients, id)
			targets = append(targets, sub)
		}
	}
	b.mu.Unlock()

	for _, sub := range targets {
		sub.once.Do(func() { close(sub.ch) })
	}
}

func (s *subscription) matches(event Event) bool {
	if s.all {
		return event.Kind == BoardCreated || event.Kind == BoardDeleted
	}
	return event.BoardID == s.boardID &&
		event.Kind != BoardCreated && event.Kind != BoardDeleted
}

// LastSeq reports the last sequence number assigned to a board. It is used by
// the WebSocket ready message; the value is a snapshot, not a lock.
func (b *Bus) LastSeq(boardID string) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq[boardID]
}
