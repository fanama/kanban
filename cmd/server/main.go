package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	primaryHTTP "kanban-app/internal/adapters/primary/http"
	"kanban-app/internal/events"
	"kanban-app/internal/usecase"
)

const (
	defaultTemplateGlob = "internal/adapters/primary/http/templates/*.html"
	defaultStaticDir    = "internal/adapters/primary/http/static"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

// run is the composition root: it picks the storage adapter, builds the four
// application services over it and hands them to the HTTP adapter. Nothing
// below this function knows a concrete storage.
func run() error {
	ctx := context.Background()

	repo, err := buildRepo(ctx)
	if err != nil {
		return err
	}
	if closer, ok := repo.(io.Closer); ok {
		defer closer.Close()
	}

	bus := events.NewBus()
	services := usecase.NewServices(repo, bus)

	handler, err := primaryHTTP.NewHTTPHandler(
		services.Board, services.Cards, services.Columns, services.Transfer,
		envOr("TEMPLATE_GLOB", defaultTemplateGlob),
		envOr("STATIC_DIR", defaultStaticDir),
		bus,
	)
	if err != nil {
		return fmt.Errorf("template load failed: %w", err)
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	addr := ":" + envOr("PORT", "8080")
	fmt.Printf("Clean Architecture Kanban server running on %s...\n", addr)
	root := primaryHTTP.WithTiming(primaryHTTP.WithGzip(mux))
	if err := http.ListenAndServe(addr, root); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
