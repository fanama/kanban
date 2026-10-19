package http

import (
	"net/http"

	"kanban-app/internal/events"
)

// withIdentity records who made the request and which tab it came from. The
// display name is a label, not an authentication identity.
func (h *HTTPHandler) withIdentity(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := r.Header.Get("X-Kanban-Actor")
		if actor == "" {
			actor = r.FormValue("actor")
		}
		clientID := r.Header.Get("X-Kanban-Client")
		if clientID == "" {
			clientID = r.FormValue("client_id")
		}

		ctx := events.WithActor(r.Context(), actor)
		ctx = events.WithClientID(ctx, clientID)
		next(w, r.WithContext(ctx))
	}
}
