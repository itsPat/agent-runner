// Package memeventbus is an in-process implementation of ports.EventBus.
// It is suitable for Phase 1 (single runner instance); multi-instance
// deployments would need a distributed bus (Redis pub/sub, NATS, etc.).
package memeventbus

import (
	"context"
	"slices"
	"sync"

	"github.com/google/uuid"

	"github.com/itsPat/agent-runner/apps/runner/internal/domain"
	"github.com/itsPat/agent-runner/apps/runner/internal/ports"
)

// subscriberChanBuffer is how many events we can buffer per subscriber
// before Publish starts dropping for that subscriber. Tune if needed.
const subscriberChanBuffer = 64

type subscriber struct {
	runID uuid.UUID
	ch    chan domain.Event
}

// Bus is a per-run fan-out event bus kept entirely in memory.
type Bus struct {
	mu   sync.RWMutex
	subs map[uuid.UUID][]*subscriber // keyed by runID
}

// Compile-time assertion that *Bus satisfies the port.
var _ ports.EventBus = (*Bus)(nil)

func New() *Bus {
	return &Bus{subs: make(map[uuid.UUID][]*subscriber)}
}

// Publish delivers ev to every subscriber of ev.RunID using a non-blocking
// send. Subscribers that cannot keep up drop events — publishing must not
// stall on slow consumers.
func (b *Bus) Publish(_ context.Context, ev domain.Event) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, s := range b.subs[ev.RunID] {
		select {
		case s.ch <- ev:
		default:
			// Subscriber is full; drop this event for them. The executor
			// never blocks. A real production system would also count
			// drops so we could alert on laggy subscribers.
		}
	}
	return nil
}

// Subscribe registers a channel for runID. The channel is closed when ctx
// is cancelled; callers should never close it themselves.
func (b *Bus) Subscribe(ctx context.Context, runID uuid.UUID) <-chan domain.Event {
	s := &subscriber{
		runID: runID,
		ch:    make(chan domain.Event, subscriberChanBuffer),
	}

	b.mu.Lock()
	b.subs[runID] = append(b.subs[runID], s)
	b.mu.Unlock()

	// One goroutine per subscription, blocking on ctx.Done. This is the
	// "context as lifetime" pattern — no explicit Unsubscribe needed.
	go func() {
		<-ctx.Done()
		b.remove(s)
	}()

	return s.ch
}

// remove drops s from the subscriber list and closes its channel. The
// order matters: remove first, then close. If we closed first, an
// in-flight Publish holding the RLock could still try to send on a closed
// channel.
func (b *Bus) remove(s *subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()

	list := b.subs[s.runID]
	idx := slices.Index(list, s)
	if idx < 0 {
		return // already removed, idempotent
	}
	b.subs[s.runID] = slices.Delete(list, idx, idx+1)
	if len(b.subs[s.runID]) == 0 {
		delete(b.subs, s.runID)
	}
	close(s.ch)
}
