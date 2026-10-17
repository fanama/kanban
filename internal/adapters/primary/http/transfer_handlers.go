package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"kanban-app/internal/domain"
)

// maxImportSize keeps a mistaken upload from exhausting the memory.
const maxImportSize = 10 << 20 // 10 MiB

// handleExport writes one board as a downloadable JSON file.
func (h *HTTPHandler) handleExport(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	snapshot, err := h.transfer.ExportBoard(r.Context(), boardID)
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data = append(data, '\n')
	name := "kanban-board.json"
	if title, ok := h.boardName(r.Context(), boardID); ok && title != "" {
		name = fmt.Sprintf("kanban-%s.json", strings.ToLower(strings.ReplaceAll(strings.TrimSpace(title), " ", "-")))
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Write(data)
}

// handleImport replaces the selected board with a JSON file: either an
// application/json body or a multipart file upload (the browser form sends the
// latter).
func (h *HTTPHandler) handleImport(w http.ResponseWriter, r *http.Request) {
	raw, err := readBoardFile(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var snapshot domain.BoardSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		http.Error(w, "the file is not a board export: "+err.Error(), http.StatusBadRequest)
		return
	}

	boardID := h.selectBoard(w, r)
	board, err := h.transfer.ImportBoard(r.Context(), boardID, snapshot)
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderBoard(w, r, board, boardID)
}

// readBoardFile reads the JSON the client sent: an uploaded file (as the
// browser form does) or a raw JSON body (as a script would).
func readBoardFile(r *http.Request) ([]byte, error) {
	contentType := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(contentType, "application/json"):
		return io.ReadAll(io.LimitReader(r.Body, maxImportSize))

	case strings.HasPrefix(contentType, "multipart/form-data"):
		return readUploadedFile(r)

	default:
		return nil, errors.New(`send a board export: a "file" upload or application/json`)
	}
}

func readUploadedFile(r *http.Request) ([]byte, error) {
	if err := r.ParseMultipartForm(maxImportSize); err != nil {
		return nil, fmt.Errorf("reading the uploaded file: %v", err)
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		return nil, errors.New(`no file: attach the board export in the "file" field`)
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, maxImportSize))
}
