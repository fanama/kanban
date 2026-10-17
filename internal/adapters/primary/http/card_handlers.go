package http

import (
	"net/http"

	"kanban-app/internal/domain"
)

func (h *HTTPHandler) handleAddCard(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	board, err := h.cards.AddCard(r.Context(), boardID, r.FormValue("column_id"), r.FormValue("title"), domain.Priority(r.FormValue("priority")))
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderBoard(w, r, board, boardID)
}

func (h *HTTPHandler) handleDeleteCard(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	board, err := h.cards.DeleteCard(r.Context(), boardID, r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderBoard(w, r, board, boardID)
}

func (h *HTTPHandler) handleMoveCard(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	board, err := h.cards.MoveCard(r.Context(), boardID, r.FormValue("card_id"), r.FormValue("column_id"))
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderBoard(w, r, board, boardID)
}

// handleGetCard returns the read-only card fragment, used to leave edit mode.
func (h *HTTPHandler) handleGetCard(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	board, err := h.boards.GetBoard(r.Context(), boardID)
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	card, ok := board.Index()[r.PathValue("id")]
	if !ok {
		http.Error(w, domain.ErrCardNotFound.Error(), http.StatusNotFound)
		return
	}
	h.renderFragment(w, "card", cardViewOf(card, board))
}

// handleEditForm returns the card's edit form fragment.
func (h *HTTPHandler) handleEditForm(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	board, err := h.boards.GetBoard(r.Context(), boardID)
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	card, ok := board.Index()[r.PathValue("id")]
	if !ok {
		http.Error(w, domain.ErrCardNotFound.Error(), http.StatusNotFound)
		return
	}
	h.renderFragment(w, "card-form", formView{
		Card:       card,
		Candidates: candidates(card, board.All()),
		Columns:    board.OrderedColumns(),
	})
}

// handleUpdateCard saves an edited card and returns the refreshed board.
func (h *HTTPHandler) handleUpdateCard(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	boardID := h.selectBoard(w, r)
	edit := domain.Card{
		ID:          r.PathValue("id"),
		Title:       r.FormValue("title"),
		Description: r.FormValue("description"),
		Objective:   r.FormValue("objective"),
		ColumnID:    r.FormValue("column_id"),
		Priority:    domain.Priority(r.FormValue("priority")),
	}
	// The edit form always submits this field (empty when nothing is checked),
	// so an absent field means "do not touch the constraints".
	if raw, provided := r.Form["depends_on"]; provided {
		edit.DependsOn = raw
	}

	board, err := h.cards.UpdateCard(r.Context(), boardID, edit)
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderBoard(w, r, board, boardID)
}
