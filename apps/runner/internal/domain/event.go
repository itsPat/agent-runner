package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// EventKind names the kind of thing that happened. Event payloads vary by
// kind and are carried as raw JSON — consumers deserialize based on kind.
type EventKind string

const (
	EventRunStarted    EventKind = "run_started"
	EventRunCompleted  EventKind = "run_completed"
	EventRunFailed     EventKind = "run_failed"
	EventRunCancelled  EventKind = "run_cancelled"
	EventTaskStarted   EventKind = "task_started"
	EventTaskCompleted EventKind = "task_completed"
	EventTaskFailed    EventKind = "task_failed"
)

// Event describes something that happened during a run's execution. It is
// the unit produced by the executor and consumed by SSE subscribers.
//
// Seq is assigned by the EventStore at Append time and is monotonically
// increasing per run. It is the cursor value used for replay-from-point
// in the SSE stream. In-memory events not yet persisted have Seq == 0.
type Event struct {
	ID        uuid.UUID
	RunID     uuid.UUID
	TaskID    *uuid.UUID // nil for run-scoped events
	Kind      EventKind
	Payload   json.RawMessage
	CreatedAt time.Time
	Seq       int64
}

// NewEvent builds a run-scoped event with a generated ID.
func NewEvent(runID uuid.UUID, kind EventKind, payload json.RawMessage) Event {
	if payload == nil {
		payload = json.RawMessage(`{}`)
	}
	return Event{
		ID:        uuid.New(),
		RunID:     runID,
		Kind:      kind,
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	}
}

// NewTaskEvent builds a task-scoped event with a generated ID.
func NewTaskEvent(runID, taskID uuid.UUID, kind EventKind, payload json.RawMessage) Event {
	e := NewEvent(runID, kind, payload)
	e.TaskID = &taskID
	return e
}
