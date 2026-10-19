// Package http is the primary (driving) adapter: it turns HTTP requests into
// calls on the input ports and renders the HTMX response.
package http

import (
	"errors"
	"html/template"
	"net/http"

	"kanban-app/internal/events"
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
	bus       *events.Bus
}

// NewHTTPHandler parses the templates and returns a ready handler. staticDir
// is the directory served under /static/ (stylesheets and other assets).
func NewHTTPHandler(boards ports.BoardService, cards ports.CardService, columns ports.ColumnService, transfer ports.ImportExportService, tmplPath, staticDir string, buses ...*events.Bus) (*HTTPHandler, error) {
	tmpl, err := template.New("index.html").ParseGlob(tmplPath)
	if err != nil {
		return nil, err
	}
	bus := events.NewBus()
	if len(buses) > 0 && buses[0] != nil {
		bus = buses[0]
	}
	return &HTTPHandler{
		boards:    boards,
		cards:     cards,
		columns:   columns,
		transfer:  transfer,
		tmpl:      tmpl,
		staticDir: staticDir,
		bus:       bus,
	}, nil
}

// RegisterRoutes wires the routes on the given mux.
func (h *HTTPHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", h.withIdentity(h.handleGetBoard))
	mux.HandleFunc("POST /cards", h.withIdentity(h.handleAddCard))
	mux.HandleFunc("POST /cards/move", h.withIdentity(h.handleMoveCard))
	mux.HandleFunc("POST /cards/reorder", h.withIdentity(h.handleReorderCard))
	mux.HandleFunc("GET /cards/{id}", h.withIdentity(h.handleGetCard))
	mux.HandleFunc("GET /cards/{id}/edit", h.withIdentity(h.handleEditForm))
	mux.HandleFunc("POST /cards/{id}", h.withIdentity(h.handleUpdateCard))
	mux.HandleFunc("POST /cards/{id}/delete", h.withIdentity(h.handleDeleteCard))
	mux.HandleFunc("POST /columns", h.withIdentity(h.handleAddColumn))
	mux.HandleFunc("GET /columns/{id}", h.withIdentity(h.handleColumnHead))
	mux.HandleFunc("GET /columns/{id}/rename", h.withIdentity(h.handleRenameForm))
	mux.HandleFunc("POST /columns/{id}/rename", h.withIdentity(h.handleRenameColumn))
	mux.HandleFunc("POST /columns/{id}/delete", h.withIdentity(h.handleDeleteColumn))
	mux.HandleFunc("POST /boards", h.withIdentity(h.handleCreateBoard))
	mux.HandleFunc("POST /boards/{id}/delete", h.withIdentity(h.handleDeleteBoard))
	mux.HandleFunc("GET /export.json", h.handleExport)
	mux.HandleFunc("POST /import.json", h.withIdentity(h.handleImport))
	mux.HandleFunc("GET /ws", h.handleWS)

	assets := http.StripPrefix("/static/", http.FileServer(http.Dir(h.staticDir)))
	mux.Handle("GET /static/vendor/", immutable(assets))
	mux.Handle("GET /static/", cacheable(assets))
}

// cacheable marks the application assets as safe to cache for a short while:
// they change with a deploy, not with every board render.
func cacheable(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=300")
		next.ServeHTTP(w, r)
	})
}

// immutable is for the pinned browser libraries under /static/vendor/.
func immutable(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(w, r)
	})
}

// parseForm accepts both the classic urlencoded forms and the multipart body
// the browser sends for a FormData POST. Plain ParseForm deliberately ignores
// multipart, which would make every JS edit look like an empty title.
func parseForm(r *http.Request) error {
	if err := r.ParseMultipartForm(32 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		return err
	}
	return nil
}

func (h *HTTPHandler) renderFragment(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
