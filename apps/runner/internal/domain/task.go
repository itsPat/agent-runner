package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// TaskKind enumerates the executor responsible for a task. Mirrors the DB
// CHECK constraint on tasks.kind.
type TaskKind string

const (
	TaskKindAI        TaskKind = "ai"
	TaskKindFetch     TaskKind = "fetch"
	TaskKindTransform TaskKind = "transform"
)

// TaskStatus is the lifecycle state of a task. Mirrors tasks.status CHECK.
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusReady     TaskStatus = "ready"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

// Task is a node in a Run's DAG. Spec and Result are opaque JSON documents
// so task-kind-specific shapes don't leak into the domain type.
type Task struct {
	ID          uuid.UUID
	RunID       uuid.UUID
	Kind        TaskKind
	Spec        json.RawMessage
	DependsOn   []uuid.UUID
	Status      TaskStatus
	Result      json.RawMessage // nil until completed
	Error       string          // empty unless failed
	Attempts    int
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
}

// NewTask constructs a fresh pending task with a generated ID. Spec may be
// nil for kinds that need no parameters.
func NewTask(runID uuid.UUID, kind TaskKind, spec json.RawMessage, dependsOn []uuid.UUID) Task {
	if dependsOn == nil {
		dependsOn = []uuid.UUID{}
	}
	if spec == nil {
		spec = json.RawMessage(`{}`)
	}
	return Task{
		ID:        uuid.New(),
		RunID:     runID,
		Kind:      kind,
		Spec:      spec,
		DependsOn: dependsOn,
		Status:    TaskStatusPending,
		Attempts:  0,
		CreatedAt: time.Now().UTC(),
	}
}
