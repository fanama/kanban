// Package usecase implements the application layer: it orchestrates the domain
// and the output ports, and knows nothing about any concrete adapter.
package usecase

import (
	"kanban-app/internal/events"
	"kanban-app/internal/ports"
)

// Services bundles the four input-port implementations over one repository.
// Construction is explicit: the composition root decides which adapter backs
// ports.BoardRepository and hands it to NewServices.
type Services struct {
	Board    ports.BoardService
	Cards    ports.CardService
	Columns  ports.ColumnService
	Transfer ports.ImportExportService
}

// NewServices wires the shared collaborators once and returns the four
// services. The services never construct each other or an adapter.
func NewServices(repo ports.BoardRepository, publishers ...events.Publisher) Services {
	publisher := events.Publisher(events.NopPublisher{})
	if len(publishers) > 0 && publishers[0] != nil {
		publisher = publishers[0]
	}
	reader := boardReader{repo: repo}
	ids := newIDGenerator()
	cascade := cascader{repo: repo, reader: reader}

	return Services{
		Board:    &boardService{repo: repo, reader: reader, ids: ids, pub: publisher},
		Cards:    &cardService{repo: repo, reader: reader, ids: ids, cascade: cascade, pub: publisher},
		Columns:  &columnService{repo: repo, reader: reader, ids: ids, cascade: cascade, pub: publisher},
		Transfer: &transferService{repo: repo, reader: reader, pub: publisher},
	}
}
