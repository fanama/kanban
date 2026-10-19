package events

import (
	"testing"
	"time"
)

func TestBusIsolatesBoardsAndBoardList(t *testing.T) {
	bus := NewBus()
	boardA, stopA := bus.SubscribeBoard("a")
	defer stopA()
	boardB, stopB := bus.SubscribeBoard("b")
	defer stopB()
	boards, stopBoards := bus.SubscribeBoards()
	defer stopBoards()

	bus.Publish(Event{Kind: CardAdded, BoardID: "a"})
	bus.Publish(Event{Kind: CardAdded, BoardID: "b"})
	bus.Publish(Event{Kind: BoardCreated, BoardID: "c"})

	expectEvent(t, boardA, CardAdded, "a")
	expectEvent(t, boardB, CardAdded, "b")
	expectEvent(t, boards, BoardCreated, "c")
	expectEmpty(t, boardA)
	expectEmpty(t, boards)
}

func TestBusSequenceIsPerBoard(t *testing.T) {
	bus := NewBus()
	first, stopFirst := bus.SubscribeBoard("a")
	defer stopFirst()
	second, stopSecond := bus.SubscribeBoard("b")
	defer stopSecond()

	bus.Publish(Event{Kind: CardAdded, BoardID: "a"})
	bus.Publish(Event{Kind: CardAdded, BoardID: "b"})
	bus.Publish(Event{Kind: CardUpdated, BoardID: "a"})

	expectSeq(t, first, 1)
	expectSeq(t, second, 1)
	expectSeq(t, first, 2)
}

func TestBusDropsASlowSubscriber(t *testing.T) {
	bus := NewBus()
	slow, stop := bus.SubscribeBoard("a")
	defer stop()
	for i := 0; i <= subscriberBuffer; i++ {
		bus.Publish(Event{Kind: CardAdded, BoardID: "a"})
	}
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-slow:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("a full subscriber was never closed")
		}
	}
}

func TestBusUnsubscribeIsIdempotent(t *testing.T) {
	bus := NewBus()
	events, stop := bus.SubscribeBoard("a")
	stop()
	stop()

	if _, ok := <-events; ok {
		t.Fatal("expected the subscription channel to be closed")
	}
}

func expectEvent(t *testing.T, ch <-chan Event, kind Kind, boardID string) {
	t.Helper()
	select {
	case event, ok := <-ch:
		if !ok {
			t.Fatalf("subscription closed before %s on %s", kind, boardID)
		}
		if event.Kind != kind || event.BoardID != boardID {
			t.Fatalf("got %s on %s, want %s on %s", event.Kind, event.BoardID, kind, boardID)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s on %s", kind, boardID)
	}
}

func expectEmpty(t *testing.T, ch <-chan Event) {
	t.Helper()
	select {
	case event, ok := <-ch:
		if ok {
			t.Fatalf("unexpected extra event: %+v", event)
		}
	case <-time.After(20 * time.Millisecond):
	}
}

func expectSeq(t *testing.T, ch <-chan Event, want uint64) {
	t.Helper()
	select {
	case event, ok := <-ch:
		if !ok {
			t.Fatalf("subscription closed before seq %d", want)
		}
		if event.Seq != want {
			t.Fatalf("got seq %d, want %d", event.Seq, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for seq %d", want)
	}
}
