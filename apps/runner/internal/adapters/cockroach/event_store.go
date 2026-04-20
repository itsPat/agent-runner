package cockroach

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itsPat/agent-runner/apps/runner/internal/domain"
	"github.com/itsPat/agent-runner/apps/runner/internal/ports"
)

// EventStore is the pgx-backed implementation of ports.EventStore.
type EventStore struct {
	pool *pgxpool.Pool
}

// Compile-time assertion.
var _ ports.EventStore = (*EventStore)(nil)

func NewEventStore(pool *pgxpool.Pool) *EventStore {
	return &EventStore{pool: pool}
}

// Append inserts the event and back-fills ev.Seq and ev.CreatedAt with the
// values the database picked. One round-trip thanks to RETURNING.
func (s *EventStore) Append(ctx context.Context, ev *domain.Event) error {
	payload := []byte(ev.Payload)
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO events (id, run_id, task_id, kind, payload)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING seq, created_at
	`, ev.ID, ev.RunID, ev.TaskID, string(ev.Kind), payload)

	var (
		seq       int64
		createdAt time.Time
	)
	if err := row.Scan(&seq, &createdAt); err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	ev.Seq = seq
	ev.CreatedAt = createdAt
	return nil
}

// eventRow mirrors the events table for pgx scanning.
type eventRow struct {
	ID        uuid.UUID       `db:"id"`
	Seq       int64           `db:"seq"`
	RunID     uuid.UUID       `db:"run_id"`
	TaskID    *uuid.UUID      `db:"task_id"`
	Kind      string          `db:"kind"`
	Payload   json.RawMessage `db:"payload"`
	CreatedAt time.Time       `db:"created_at"`
}

func (r eventRow) toDomain() domain.Event {
	return domain.Event{
		ID:        r.ID,
		Seq:       r.Seq,
		RunID:     r.RunID,
		TaskID:    r.TaskID,
		Kind:      domain.EventKind(r.Kind),
		Payload:   r.Payload,
		CreatedAt: r.CreatedAt,
	}
}

// ListSince range-scans the (run_id, seq) index.
func (s *EventStore) ListSince(ctx context.Context, runID uuid.UUID, afterSeq int64) ([]domain.Event, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, seq, run_id, task_id, kind, payload, created_at
		FROM events
		WHERE run_id = $1 AND seq > $2
		ORDER BY seq ASC
	`, runID, afterSeq)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	rs, err := pgx.CollectRows(rows, pgx.RowToStructByName[eventRow])
	if err != nil {
		return nil, fmt.Errorf("scan events: %w", err)
	}
	out := make([]domain.Event, len(rs))
	for i, r := range rs {
		out[i] = r.toDomain()
	}
	return out, nil
}
