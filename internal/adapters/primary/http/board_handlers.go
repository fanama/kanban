package http

import (
	"context"
	"encoding/json"
	"net/http"

	"kanban-app/internal/domain"
)

// boardCookie remembers which board the browser is working on; a fresh visit
// without a cookie lands on the first board.
const boardCookie = "kanban_board"

// selectBoard resolves the board a request addresses: an explicit ?board=
// (board switching) wins, then the saved cookie, then the first board. The
// cookie is set whenever the user switches, so following links keeps them.
func (h *HTTPHandler) selectBoard(w http.ResponseWriter, r *http.Request) string {
	boards, _ := h.boards.ListBoards(r.Context())
	byID := make(map[string]domain.Board, len(boards))
	for _, board := range boards {
		byID[board.ID] = board
	}

	if asked := r.URL.Query().Get("board"); asked != "" {
		if board, ok := byID[asked]; ok {
			http.SetCookie(w, &http.Cookie{Name: boardCookie, Value: board.ID, Path: "/"})
			return board.ID
		}
	}
	if cookie, err := r.Cookie(boardCookie); err == nil {
		if board, ok := byID[cookie.Value]; ok {
			return board.ID
		}
	}
	if len(boards) > 0 {
		return boards[0].ID
	}
	return ""
}

// renderBoard renders a board page (or the swapped fragment) around the board
// the use case just returned, with the boards bar riding along.
func (h *HTTPHandler) renderBoard(w http.ResponseWriter, r *http.Request, board domain.BoardView, boardID string) {
	boards, err := h.boards.ListBoards(r.Context())
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	name := "index.html"
	if r.Header.Get("HX-Request") == "true" {
		name = "board"
	}
	h.renderFragment(w, name, toView(board, boards, boardID))
}

func (h *HTTPHandler) handleGetBoard(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)

	board, err := h.boards.GetBoard(r.Context(), boardID)
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderBoard(w, r, board, boardID)
}

// handleCreateBoard appends a board and puts the user in front of it.
func (h *HTTPHandler) handleCreateBoard(w http.ResponseWriter, r *http.Request) {
	board, err := h.boards.CreateBoard(r.Context(), r.FormValue("name"))
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	if h.renderRedirect(w, r, "/?board="+board.ID) {
		return
	}
	r.URL.RawQuery = "board=" + board.ID
	h.handleGetBoard(w, r)
}

// handleDeleteBoard removes a non-last board and passes the baton to another
// one, keeping the browser on a working page.
func (h *HTTPHandler) handleDeleteBoard(w http.ResponseWriter, r *http.Request) {
	if err := h.boards.DeleteBoard(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	r.URL.RawQuery = ""
	// Forget the cookie of the board that just vanished; handleGetBoard falls
	// back to the first survivor anyway.
	http.SetCookie(w, &http.Cookie{Name: boardCookie, Value: "", Path: "/", MaxAge: -1})
	if isPatchRequest(r) {
		fallback := h.selectBoard(w, r)
		url := "/"
		if fallback != "" {
			url = "/?board=" + fallback
		}
		h.renderRedirect(w, r, url)
		return
	}
	h.handleGetBoard(w, r)
}

// renderRedirect answers a JS mutation that must navigate after success: the
// optimistic sidebar was already updated client-side, so this is just the URL.
func (h *HTTPHandler) renderRedirect(w http.ResponseWriter, r *http.Request, url string) bool {
	if !isPatchRequest(r) {
		return false
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(map[string]string{"type": "redirect", "url": url}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	return true
}

// boardName reads a board's title (used to name the downloaded file).
func (h *HTTPHandler) boardName(ctx context.Context, boardID string) (string, bool) {
	boards, err := h.boards.ListBoards(ctx)
	if err != nil {
		return "", false
	}
	for _, board := range boards {
		if board.ID == boardID {
			return board.Name, true
		}
	}
	return "", false
}
