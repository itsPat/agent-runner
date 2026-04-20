// Package domain holds the entities and invariants at the heart of the
// runner. Nothing in this package imports infrastructure (no SQL, no HTTP,
// no protobuf). Ports and adapters depend on domain, never the reverse.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// RunStatus is the lifecycle state of a run. Values mirror the DB CHECK
// constraint on runs.status — keep them in sync by hand until we lift it
// into a shared constant.
type RunStatus string

const (
	RunStatusPending   RunStatus = "pending"
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCancelled RunStatus = "cancelled"
)

// Run is a single user-submitted goal being decomposed and executed.
type Run struct {
	ID          uuid.UUID
	Goal        string
	Status      RunStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time // nil until terminal
}

// NewRun constructs a fresh pending run with a generated ID.
func NewRun(goal string) Run {
	now := time.Now().UTC()
	return Run{
		ID:        uuid.New(),
		Goal:      goal,
		Status:    RunStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
