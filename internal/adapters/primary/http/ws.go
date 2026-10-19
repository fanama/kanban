package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"kanban-app/internal/events"
	"kanban-app/internal/ports"
)

const wsHeartbeat = 30 * time.Second

type wsClient struct {
	handler  *HTTPHandler
	conn     *websocket.Conn
	boardID  string
	clientID string
}

func (h *HTTPHandler) handleWS(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-origin websocket refused", http.StatusForbidden)
		return
	}
	boardID := strings.TrimSpace(r.URL.Query().Get("board"))
	clientID := cleanToken(r.URL.Query().Get("client"))
	if boardID == "" {
		http.Error(w, "board is required", http.StatusBadRequest)
		return
	}
	if _, err := h.boards.GetBoard(r.Context(), boardID); err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // same-origin was checked above
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(1024)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	client := &wsClient{handler: h, conn: conn, boardID: boardID, clientID: clientID}
	defer conn.Close(websocket.StatusNormalClosure, "")
	client.run(ctx, cancel)
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func cleanToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > 80 {
		raw = raw[:80]
	}
	return raw
}

func (c *wsClient) run(ctx context.Context, cancel context.CancelFunc) {
	boardEvents, stopBoard := c.handler.bus.SubscribeBoard(c.boardID)
	defer stopBoard()
	boardListEvents, stopBoards := c.handler.bus.SubscribeBoards()
	defer stopBoards()
	if !c.ready(ctx) {
		return
	}
	go c.readLoop(ctx, cancel)
	ticker := time.NewTicker(wsHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case event, ok := <-boardEvents:
			if !ok || c.handleBoardEvent(ctx, event) {
				return
			}
		case event, ok := <-boardListEvents:
			if !ok || c.handleBoardListEvent(ctx, event) {
				return
			}
		case <-ticker.C:
			if !c.ping(ctx) {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (c *wsClient) ready(ctx context.Context) bool {
	return c.writeJSON(ctx, map[string]any{
		"type": "ready", "board_id": c.boardID,
		"seq": c.handler.bus.LastSeq(c.boardID),
	}) == nil
}

func (c *wsClient) ping(ctx context.Context) bool {
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.conn.Ping(pingCtx) == nil
}

func (c *wsClient) readLoop(ctx context.Context, cancel context.CancelFunc) {
	defer cancel()
	for {
		if _, _, err := c.conn.Read(ctx); err != nil {
			return
		}
	}
}

func (c *wsClient) handleBoardEvent(ctx context.Context, event events.Event) bool {
	if event.ClientID != "" && event.ClientID == c.clientID {
		return false
	}
	board, err := c.handler.boards.GetBoard(ctx, c.boardID)
	if err != nil {
		_ = c.writeJSON(ctx, patchEnvelope{
			Type: "patch", BoardID: c.boardID, Event: string(event.Kind),
			Reload: true, Notice: event.Notice,
		})
		return true
	}
	change := ports.BoardChange{
		Board: board, Kind: event.Kind, CardIDs: event.CardIDs,
		ColumnIDs: event.ColumnIDs, RemovedCards: event.RemovedCards,
		RemovedColumns: event.RemovedColumns, Reload: event.Reload,
		Notice: event.Notice,
	}
	envelope, err := c.handler.buildPatch(change, c.boardID, event.ClientID)
	if err != nil {
		return false
	}
	envelope.Seq = event.Seq
	envelope.Actor = event.Actor
	if err := c.writeJSON(ctx, envelope); err != nil {
		return true
	}
	return event.Kind == events.BoardDeleted && event.BoardID == c.boardID
}

func (c *wsClient) handleBoardListEvent(ctx context.Context, event events.Event) bool {
	if event.ClientID != "" && event.ClientID == c.clientID {
		return false
	}
	boards, err := c.handler.boards.ListBoards(ctx)
	if err != nil {
		return false
	}
	view := boardView{BoardID: c.boardID, Boards: boards}
	html, err := c.handler.renderSidebar(view)
	if err != nil {
		return false
	}
	envelope := patchEnvelope{
		Type: "patch", BoardID: event.BoardID, Event: string(event.Kind),
		Actor: event.Actor, ClientID: event.ClientID, Notice: event.Notice,
		Fragments: []patchFragment{{Target: "sidebar-boards", HTML: html}},
	}
	if err := c.writeJSON(ctx, envelope); err != nil {
		return true
	}
	return event.Kind == events.BoardDeleted && event.BoardID == c.boardID
}

func (c *wsClient) writeJSON(ctx context.Context, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("websocket marshal: %w", err)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.conn.Write(writeCtx, websocket.MessageText, data)
}
