package http

import (
	"context"
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
	h.handleGetBoard(w, r)
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
