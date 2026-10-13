// Package http is the primary (driving) adapter: it turns HTTP requests into
// calls on the BoardUseCase input port and renders the HTMX response.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strings"

	"kanban-app/internal/domain"
	"kanban-app/internal/ports"
)

// boardCookie remembers which board the browser is working on; a fresh visit
// without a cookie lands on the first board.
const boardCookie = "kanban_board"

// maxImportSize keeps a mistaken upload from exhausting the memory.
const maxImportSize = 10 << 20 // 10 MiB

// HTTPHandler adapts the application core to the web.
type HTTPHandler struct {
	useCase ports.BoardUseCase
	tmpl    *template.Template
}

// cardView is a card plus what the UI needs about its neighbours: the stage it
// may go back to, the one it may advance to, and what holds it there. Blockers
// are the constraints that are not far enough; Teamed are the cards that depend
// on this one and have not caught up.
type cardView struct {
	domain.Card
	Prev     *domain.Column
	Next     *domain.Column
	Blockers []domain.Card
	Teamed   []domain.Card
}

// columnView is one column of the board, ready to render.
type columnView struct {
	Column    domain.Column
	Cards     []cardView
	CanDelete bool
}

// boardView is one board page: the sidebar (with its sibling boards) and the
// ordered list of columns with their cards.
type boardView struct {
	BoardID string
	Board   domain.Board
	Boards  []domain.Board
	Columns []columnView
}

// formView is the card being edited, the cards it may depend on, and the stages
// it may be placed in.
type formView struct {
	domain.Card
	Candidates []domain.Card
	Columns    []domain.Column
}

// Selected reports whether a candidate is already a constraint of the card.
func (f formView) Selected(id string) bool { return f.DependsOnCard(id) }

// ColumnName resolves a column ID to its name, for the candidate list.
func (f formView) ColumnName(id string) string {
	for _, column := range f.Columns {
		if column.ID == id {
			return column.Name
		}
	}
	return id
}

// NewHTTPHandler parses the templates and returns a ready handler.
func NewHTTPHandler(useCase ports.BoardUseCase, tmplPath string) (*HTTPHandler, error) {
	tmpl, err := template.New("index.html").ParseGlob(tmplPath)
	if err != nil {
		return nil, err
	}
	return &HTTPHandler{useCase: useCase, tmpl: tmpl}, nil
}

// RegisterRoutes wires the routes on the given mux.
func (h *HTTPHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", h.handleGetBoard)
	mux.HandleFunc("POST /cards", h.handleAddCard)
	mux.HandleFunc("POST /cards/move", h.handleMoveCard)
	mux.HandleFunc("GET /cards/{id}", h.handleGetCard)
	mux.HandleFunc("GET /cards/{id}/edit", h.handleEditForm)
	mux.HandleFunc("POST /cards/{id}", h.handleUpdateCard)
	mux.HandleFunc("POST /cards/{id}/delete", h.handleDeleteCard)
	mux.HandleFunc("POST /columns", h.handleAddColumn)
	mux.HandleFunc("GET /columns/{id}", h.handleColumnHead)
	mux.HandleFunc("GET /columns/{id}/rename", h.handleRenameForm)
	mux.HandleFunc("POST /columns/{id}/rename", h.handleRenameColumn)
	mux.HandleFunc("POST /columns/{id}/delete", h.handleDeleteColumn)
	mux.HandleFunc("POST /boards", h.handleCreateBoard)
	mux.HandleFunc("POST /boards/{id}/delete", h.handleDeleteBoard)
	mux.HandleFunc("GET /export.json", h.handleExport)
	mux.HandleFunc("POST /import.json", h.handleImport)
}

// selectBoard resolves the board a request addresses: an explicit ?board=
// (board switching) wins, then the saved cookie, then the first board. The
// cookie is set whenever the user switches, so following links keeps them.
func (h *HTTPHandler) selectBoard(w http.ResponseWriter, r *http.Request) string {
	boards, _ := h.useCase.ListBoards(r.Context())
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
	boards, err := h.useCase.ListBoards(r.Context())
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

	board, err := h.useCase.GetBoard(r.Context(), boardID)
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderBoard(w, r, board, boardID)
}

// handleCreateBoard appends a board and puts the user in front of it.
func (h *HTTPHandler) handleCreateBoard(w http.ResponseWriter, r *http.Request) {
	board, err := h.useCase.CreateBoard(r.Context(), r.FormValue("name"))
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
	if err := h.useCase.DeleteBoard(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	r.URL.RawQuery = ""
	// Forget the cookie of the board that just vanished; handleGetBoard falls
	// back to the first survivor anyway.
	http.SetCookie(w, &http.Cookie{Name: boardCookie, Value: "", Path: "/", MaxAge: -1})
	h.handleGetBoard(w, r)
}

func (h *HTTPHandler) handleAddCard(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	board, err := h.useCase.AddCard(r.Context(), boardID, r.FormValue("title"), domain.Priority(r.FormValue("priority")))
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderBoard(w, r, board, boardID)
}

func (h *HTTPHandler) handleDeleteCard(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	board, err := h.useCase.DeleteCard(r.Context(), boardID, r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderBoard(w, r, board, boardID)
}

func (h *HTTPHandler) handleMoveCard(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	board, err := h.useCase.MoveCard(r.Context(), boardID, r.FormValue("card_id"), r.FormValue("column_id"))
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderBoard(w, r, board, boardID)
}

// handleGetCard returns the read-only card fragment, used to leave edit mode.
func (h *HTTPHandler) handleGetCard(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	board, err := h.useCase.GetBoard(r.Context(), boardID)
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
	board, err := h.useCase.GetBoard(r.Context(), boardID)
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

	board, err := h.useCase.UpdateCard(r.Context(), boardID, edit)
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderBoard(w, r, board, boardID)
}

func (h *HTTPHandler) handleAddColumn(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	board, err := h.useCase.AddColumn(r.Context(), boardID, r.FormValue("name"))
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderBoard(w, r, board, boardID)
}

// columnViewOf locates a column's rendered head in the current board, for the
// fragments (the read-only header and the rename form). It reports false when
// the column is not on the board.
func (h *HTTPHandler) columnViewOf(w http.ResponseWriter, r *http.Request) (columnView, bool) {
	boardID := h.selectBoard(w, r)
	board, err := h.useCase.GetBoard(r.Context(), boardID)
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
	board, err := h.useCase.RenameColumn(r.Context(), boardID, r.PathValue("id"), r.FormValue("name"))
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderBoard(w, r, board, boardID)
}

func (h *HTTPHandler) handleDeleteColumn(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	board, err := h.useCase.DeleteColumn(r.Context(), boardID, r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	h.renderBoard(w, r, board, boardID)
}

// handleExport writes one board as a downloadable JSON file.
func (h *HTTPHandler) handleExport(w http.ResponseWriter, r *http.Request) {
	boardID := h.selectBoard(w, r)
	snapshot, err := h.useCase.ExportBoard(r.Context(), boardID)
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

// boardName reads a board's title (used to name the downloaded file).
func (h *HTTPHandler) boardName(ctx context.Context, boardID string) (string, bool) {
	boards, err := h.useCase.ListBoards(ctx)
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
	board, err := h.useCase.ImportBoard(r.Context(), boardID, snapshot)
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
		if err := r.ParseMultipartForm(maxImportSize); err != nil {
			return nil, fmt.Errorf("reading the uploaded file: %v", err)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			return nil, errors.New(`no file: attach the board export in the "file" field`)
		}
		defer file.Close()
		return io.ReadAll(io.LimitReader(file, maxImportSize))

	default:
		return nil, errors.New(`send a board export: a "file" upload or application/json`)
	}
}

// candidates lists the cards a card may depend on: all the others.
func candidates(card domain.Card, all []domain.Card) []domain.Card {
	others := make([]domain.Card, 0, len(all))
	for _, c := range all {
		if c.ID != card.ID {
			others = append(others, c)
		}
	}
	return others
}

func (h *HTTPHandler) renderFragment(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// toView decorates every card with the stage rules that apply to it: the
// constraints that hold it back, the dependents that hold it down, and the
// neighbours it may move to. The boards bar rides along.
func toView(board domain.BoardView, boards []domain.Board, boardID string) boardView {
	view := boardView{BoardID: boardID, Boards: boards, Columns: make([]columnView, 0, len(board.Columns))}

	for _, b := range boards {
		if b.ID == boardID {
			view.Board = b
		}
	}

	for i, column := range board.Columns {
		rendered := columnView{Column: column.Column, CanDelete: len(board.Columns) > 1}

		for _, card := range column.Cards {
			cardView := cardView{Card: card}

			if i > 0 {
				previous := board.Columns[i-1].Column
				cardView.Prev = &previous
			}
			if i+1 < len(board.Columns) {
				next := board.Columns[i+1].Column
				cardView.Next = &next
				cardView.Blockers = domain.Unfinished(card, next.Position, board)
				// The dependents are normally pulled along; only a pull impossible
				// by the stage rules leaves someone waiting, so plan the pull to
				// know whether the button must be locked.
				if _, err := domain.PullPlan(card, next.Position, board); err != nil {
					cardView.Teamed = domain.AheadOfDependents(card, next.Position, board)
				}
			}

			rendered.Cards = append(rendered.Cards, cardView)
		}
		view.Columns = append(view.Columns, rendered)
	}

	return view
}

// cardViewOf renders a single card with the same decoration as the board.
// (see toView)
func cardViewOf(card domain.Card, board domain.BoardView) cardView {
	for _, column := range toView(board, nil, "").Columns {
		for _, rendered := range column.Cards {
			if rendered.ID == card.ID {
				return rendered
			}
		}
	}
	return cardView{Card: card}
}

// statusFor maps a domain error to the HTTP status the client should see.
func statusFor(err error) int {
	switch {
	case errors.Is(err, domain.ErrCardNotFound), errors.Is(err, domain.ErrColumnNotFound), errors.Is(err, domain.ErrBoardNotFound):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrEmptyTitle),
		errors.Is(err, domain.ErrCardBlocked),
		errors.Is(err, domain.ErrWouldLeaveDependents),
		errors.Is(err, domain.ErrUnknownDependency),
		errors.Is(err, domain.ErrSelfDependency),
		errors.Is(err, domain.ErrDependencyCycle),
		errors.Is(err, domain.ErrEmptyColumnName),
		errors.Is(err, domain.ErrColumnExists),
		errors.Is(err, domain.ErrLastColumn),
		errors.Is(err, domain.ErrInvalidImport),
		errors.Is(err, domain.ErrEmptyBoardName),
		errors.Is(err, domain.ErrBoardExists),
		errors.Is(err, domain.ErrLastBoard),
		errors.Is(err, domain.ErrBoardsUnsupported):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
