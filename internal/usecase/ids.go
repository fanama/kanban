package usecase

import (
	"fmt"
	"sync/atomic"
	"time"
)

// idGenerator is shared by every service so board, card and column IDs come
// from one sequence and can never collide.
type idGenerator struct {
	sequence atomic.Int64
}

func newIDGenerator() *idGenerator {
	gen := &idGenerator{}
	gen.sequence.Store(time.Now().UnixNano())
	return gen
}

func (g *idGenerator) next(prefix string) string {
	n := g.sequence.Add(1)
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().Unix(), n)
}
