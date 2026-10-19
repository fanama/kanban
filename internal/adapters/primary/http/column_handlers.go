package http

import (
	"net/http"

	"kanban-app/internal/domain"
)

func (h *HTTPHandler) handleAddColumn(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	change, err := h.columns.AddColumn(r.Context(), boardID, r.FormValue("name"))
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderChange(w, r, change, boardID)
}

// columnViewOf locates a column's rendered head in the current board, for the
// fragments (the read-only header and the rename form). It reports false when
// the column is not on the board.
func (h *HTTPHandler) columnViewOf(w http.ResponseWriter, r *http.Request) (columnView, bool) {
	boardID := h.selectBoard(w, r)
	board, err := h.boards.GetBoard(r.Context(), boardID)
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return columnView{}, false
	}
	for _, column := range toView(board, nil, boardID).Columns {
		if column.Column.ID == r.PathValue("id") {
			return column, true
		}
	}
	http.Error(w, domain.ErrColumnNotFound.Error(), statusFor(domain.ErrColumnNotFound))
	return columnView{}, false
}

// handleColumnHead returns a column's read-only header fragment: it takes the
// column out of rename mode.
func (h *HTTPHandler) handleColumnHead(w http.ResponseWriter, r *http.Request) {
	column, ok := h.columnViewOf(w, r)
	if !ok {
		return
	}
	h.renderFragment(w, "column-head", column)
}

// handleRenameForm returns the header fragment with an inline rename form.
func (h *HTTPHandler) handleRenameForm(w http.ResponseWriter, r *http.Request) {
	column, ok := h.columnViewOf(w, r)
	if !ok {
		return
	}
	h.renderFragment(w, "column-head-edit", column)
}

// handleRenameColumn saves a renamed column and returns the refreshed board.
func (h *HTTPHandler) handleRenameColumn(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	change, err := h.columns.RenameColumn(r.Context(), boardID, r.PathValue("id"), r.FormValue("name"))
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderChange(w, r, change, boardID)
}

func (h *HTTPHandler) handleDeleteColumn(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	change, err := h.columns.DeleteColumn(r.Context(), boardID, r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderChange(w, r, change, boardID)
}
