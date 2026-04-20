package ports

import (
	"context"

	"github.com/google/uuid"

	"github.com/itsPat/agent-runner/apps/runner/internal/domain"
)

// EventBus is the pub/sub abstraction used by the executor (publisher) and
// the SSE handler (subscriber). Implementations are expected to fan out
// events to every subscriber that asked for the given run id, with
// best-effort delivery — a slow subscriber should not block the publisher.
type EventBus interface {
	// Publish delivers the event to current subscribers of ev.RunID.
	// It must not block on slow subscribers; implementations should drop
	// events for laggers rather than stall the caller.
	Publish(ctx context.Context, ev domain.Event) error

	// Subscribe returns a channel that receives every Event published for
	// runID while ctx is live. The implementation closes the channel when
	// ctx is cancelled or the bus is shut down. Callers must not close it.
	Subscribe(ctx context.Context, runID uuid.UUID) <-chan domain.Event
}
