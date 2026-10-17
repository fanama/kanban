// Package http is the primary (driving) adapter: it turns HTTP requests into
// calls on the input ports and renders the HTMX response.
package http

import (
	"html/template"
	"net/http"

	"kanban-app/internal/ports"
)

// HTTPHandler adapts the application services to the web. Each service is a
// small input port injected by the composition root; the handler never builds
// an adapter or another service.
type HTTPHandler struct {
	boards    ports.BoardService
	cards     ports.CardService
	columns   ports.ColumnService
	transfer  ports.ImportExportService
	tmpl      *template.Template
	staticDir string
}

// NewHTTPHandler parses the templates and returns a ready handler. staticDir
// is the directory served under /static/ (stylesheets and other assets).
func NewHTTPHandler(boards ports.BoardService, cards ports.CardService, columns ports.ColumnService, transfer ports.ImportExportService, tmplPath, staticDir string) (*HTTPHandler, error) {
	tmpl, err := template.New("index.html").ParseGlob(tmplPath)
	if err != nil {
		return nil, err
	}
	return &HTTPHandler{
		boards:    boards,
		cards:     cards,
		columns:   columns,
		transfer:  transfer,
		tmpl:      tmpl,
		staticDir: staticDir,
	}, nil
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

	assets := http.StripPrefix("/static/", http.FileServer(http.Dir(h.staticDir)))
	mux.Handle("GET /static/", cacheable(assets))
}

// cacheable marks the static assets as safe to cache for a short while: the
// stylesheet changes with a deploy, not with every board render.
func cacheable(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

func (h *HTTPHandler) renderFragment(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
