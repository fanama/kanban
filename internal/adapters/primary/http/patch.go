package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"

	"kanban-app/internal/events"
	"kanban-app/internal/ports"
)

// patchEnvelope is the JSON payload shared by X-Kanban-Patch responses and
// WebSocket broadcasts.
type patchEnvelope struct {
	Type      string          `json:"type"`
	BoardID   string          `json:"board_id"`
	Seq       uint64          `json:"seq"`
	Event     string          `json:"event"`
	Actor     string          `json:"actor,omitempty"`
	ClientID  string          `json:"client_id,omitempty"`
	Fragments []patchFragment `json:"fragments,omitempty"`
	Remove    []patchRemove   `json:"remove,omitempty"`
	Reload    bool            `json:"reload,omitempty"`
	Notice    events.Notice   `json:"notice"`
}

type patchFragment struct {
	Target string `json:"target"`
	ID     string `json:"id,omitempty"`
	HTML   string `json:"html"`
}

type patchRemove struct {
	Target string `json:"target"`
	ID     string `json:"id"`
}

// renderChange answers a mutation with a JSON patch for the JS client, the
// board fragment for HTMX, or the full page for a classic post.
func isPatchRequest(r *http.Request) bool {
	return r.Header.Get("X-Kanban-Patch") == "true"
}

func (h *HTTPHandler) renderChange(w http.ResponseWriter, r *http.Request, change ports.BoardChange, boardID string) {
	if isPatchRequest(r) {
		h.renderPatch(w, r, change, boardID)
		return
	}
	h.renderBoard(w, r, change.Board, boardID)
}

func (h *HTTPHandler) renderPatch(w http.ResponseWriter, r *http.Request, change ports.BoardChange, boardID string) {
	envelope, err := h.buildPatch(change, boardID, events.ClientID(r.Context()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(envelope); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *HTTPHandler) buildPatch(change ports.BoardChange, boardID, clientID string) (patchEnvelope, error) {
	envelope := patchEnvelope{
		Type: "patch", BoardID: boardID, Event: string(change.Kind),
		Actor: change.Notice.Actor, ClientID: clientID, Notice: change.Notice,
	}
	if change.Reload {
		envelope.Reload = true
		return envelope, nil
	}
	view := toView(change.Board, nil, boardID)
	if err := h.addCardFragments(&envelope, view, change); err != nil {
		return patchEnvelope{}, err
	}
	if err := h.addColumnFragments(&envelope, view, change); err != nil {
		return patchEnvelope{}, err
	}
	if err := h.addSummary(&envelope, view); err != nil {
		return patchEnvelope{}, err
	}
	return envelope, nil
}

func (h *HTTPHandler) addCardFragments(envelope *patchEnvelope, view boardView, change ports.BoardChange) error {
	for _, id := range change.RemovedCards {
		envelope.Remove = append(envelope.Remove, patchRemove{Target: "card", ID: id})
	}
	for _, id := range change.CardIDs {
		html, ok, err := h.cardFragment(view, id)
		if err != nil {
			return err
		}
		if !ok {
			envelope.Remove = append(envelope.Remove, patchRemove{Target: "card", ID: id})
			continue
		}
		envelope.Fragments = append(envelope.Fragments, patchFragment{
			Target: "card", ID: id, HTML: html,
		})
	}
	return nil
}

func (h *HTTPHandler) addColumnFragments(envelope *patchEnvelope, view boardView, change ports.BoardChange) error {
	for _, id := range change.RemovedColumns {
		envelope.Remove = append(envelope.Remove, patchRemove{Target: "column", ID: id})
	}
	for _, id := range change.ColumnIDs {
		column, ok := columnOfView(view, id)
		if !ok {
			continue
		}
		head, err := h.renderColumnHead(column)
		if err != nil {
			return err
		}
		envelope.Fragments = append(envelope.Fragments, patchFragment{
			Target: "column-head", ID: id, HTML: head,
		})
		if change.Kind == events.CardMoved || change.Kind == events.CardDeleted ||
			change.Kind == events.ColumnAdded || change.Kind == events.ColumnDeleted ||
			change.Kind == events.ColumnRenamed {
			cards, err := h.renderColumnCards(column)
			if err != nil {
				return err
			}
			envelope.Fragments = append(envelope.Fragments, patchFragment{
				Target: "column-cards", ID: id, HTML: cards,
			})
		}
	}
	return nil
}

func (h *HTTPHandler) addSummary(envelope *patchEnvelope, view boardView) error {
	var buf bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&buf, "board-summary", view); err != nil {
		return err
	}
	envelope.Fragments = append(envelope.Fragments, patchFragment{
		Target: "board-summary", HTML: buf.String(),
	})
	return nil
}

func (h *HTTPHandler) cardFragment(view boardView, id string) (string, bool, error) {
	for _, column := range view.Columns {
		for _, card := range column.Cards {
			if card.ID != id {
				continue
			}
			var buf bytes.Buffer
			if err := h.tmpl.ExecuteTemplate(&buf, "card", card); err != nil {
				return "", false, err
			}
			return buf.String(), true, nil
		}
	}
	return "", false, nil
}

func (h *HTTPHandler) renderColumnHead(column columnView) (string, error) {
	var buf bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&buf, "column-head", column); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (h *HTTPHandler) renderColumnCards(column columnView) (string, error) {
	var buf bytes.Buffer
	for _, card := range column.Cards {
		if err := h.tmpl.ExecuteTemplate(&buf, "card", card); err != nil {
			return "", err
		}
	}
	if len(column.Cards) == 0 {
		buf.WriteString(`<p class="empty">No cards yet</p>`)
	}
	return buf.String(), nil
}

func (h *HTTPHandler) renderSidebar(view boardView) (string, error) {
	var buf bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&buf, "sidebar-boards", view); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func columnOfView(view boardView, id string) (columnView, bool) {
	for _, column := range view.Columns {
		if column.Column.ID == id {
			return column, true
		}
	}
	return columnView{}, false
}

func atoiOrZero(raw string) int {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}
