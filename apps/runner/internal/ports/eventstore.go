package ports

import (
	"context"

	"github.com/google/uuid"

	"github.com/itsPat/agent-runner/apps/runner/internal/domain"
)

// EventStore persists events and lets readers query history. It is the
// durable complement to EventBus: the bus tells you when new events
// happen; the store lets you ask what already happened.
type EventStore interface {
	// Append persists the event and populates ev.Seq and ev.CreatedAt
	// with the values assigned by the store.
	Append(ctx context.Context, ev *domain.Event) error

	// ListSince returns every event for runID with Seq strictly greater
	// than afterSeq, ordered by Seq ascending. Pass 0 to get the full
	// history.
	ListSince(ctx context.Context, runID uuid.UUID, afterSeq int64) ([]domain.Event, error)
}
