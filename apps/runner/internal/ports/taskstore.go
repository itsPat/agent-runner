// Package ports declares the interfaces the runner's core needs from the
// outside world. The app layer depends only on ports; adapters implement
// them. Composition happens in cmd/server/main.go.
package ports

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/itsPat/agent-runner/apps/runner/internal/domain"
)

// ErrNotFound is returned by stores when a requested row is absent.
// Adapters should wrap their driver-specific "no rows" error with this so
// callers can check via errors.Is without importing driver packages.
var ErrNotFound = errors.New("not found")

// TaskStore persists and loads Runs and their Tasks.
//
// The interface is intentionally small: methods are added when a caller
// needs them, not speculatively. Phase 1 needs CreateRun (for POST /runs),
// GetRun (for GET /runs/:id), and ListTasks (for the run detail view).
// Phase 1.4 will grow the interface with MarkTaskRunning, MarkTaskCompleted,
// and ListReadyTasks as the executor needs them.
type TaskStore interface {
	// CreateRun atomically inserts the run and all of its tasks.
	// The DAG's invariants are already validated by NewDAG.
	CreateRun(ctx context.Context, dag domain.DAG) error

	// GetRun returns a run by id, or ErrNotFound.
	GetRun(ctx context.Context, id uuid.UUID) (domain.Run, error)

	// ListTasks returns every task belonging to the given run, ordered by
	// creation time. An empty slice is returned for unknown runs — callers
	// should GetRun first if they need to distinguish "no tasks" from
	// "no run."
	ListTasks(ctx context.Context, runID uuid.UUID) ([]domain.Task, error)

	// MarkRunRunning sets a pending run's status to running.
	MarkRunRunning(ctx context.Context, runID uuid.UUID, at time.Time) error

	// MarkRunCompleted sets status to completed and stamps completed_at.
	MarkRunCompleted(ctx context.Context, runID uuid.UUID, at time.Time) error

	// MarkRunFailed sets status to failed and stamps completed_at.
	MarkRunFailed(ctx context.Context, runID uuid.UUID, at time.Time) error

	// MarkTaskRunning sets a task's status to running, stamps started_at,
	// and increments attempts.
	MarkTaskRunning(ctx context.Context, taskID uuid.UUID, at time.Time) error

	// MarkTaskCompleted sets status to completed, stamps completed_at,
	// and persists the task's result payload.
	MarkTaskCompleted(ctx context.Context, taskID uuid.UUID, result json.RawMessage, at time.Time) error

	// MarkTaskFailed sets status to failed, stamps completed_at, and
	// persists the error message.
	MarkTaskFailed(ctx context.Context, taskID uuid.UUID, errMsg string, at time.Time) error
}
