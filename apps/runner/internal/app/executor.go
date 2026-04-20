package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/itsPat/agent-runner/apps/runner/internal/domain"
	"github.com/itsPat/agent-runner/apps/runner/internal/ports"
)

// Executor runs a DAG to completion. Phase 1 implementation:
//
//   - Single goroutine per run (no worker pool — that's Phase 3).
//   - Tasks execute in topological order, one at a time.
//   - Each task "executes" by sleeping for taskDuration and producing a
//     synthetic result. Real task execution arrives in Phase 3.
//   - DB state and the event stream are kept in sync by updating the DB
//     first and only then publishing; a dropped event leaves the DB
//     correct, never the other way around.
type Executor struct {
	store        ports.TaskStore
	events       ports.EventStore
	bus          ports.EventBus
	taskDuration time.Duration
	runBudget    time.Duration
}

// NewExecutor constructs an executor with sensible Phase 1 timings.
func NewExecutor(store ports.TaskStore, events ports.EventStore, bus ports.EventBus) *Executor {
	return &Executor{
		store:        store,
		events:       events,
		bus:          bus,
		taskDuration: 1500 * time.Millisecond,
		runBudget:    2 * time.Minute,
	}
}

// emit persists the event then broadcasts it. Persist-first means a
// dropped broadcast leaves the durable record intact — a client reading
// from the store via cursor resume will still see the event. The reverse
// order would risk broadcasting state that never made it to disk.
//
// Returns the persisted event's assigned Seq for callers that want to
// log or correlate.
func (e *Executor) emit(ctx context.Context, ev domain.Event) int64 {
	log := slog.With("run_id", ev.RunID, "kind", ev.Kind)
	if err := e.events.Append(ctx, &ev); err != nil {
		log.Error("persist event", "err", err)
		// Best-effort broadcast even if persistence failed; at least live
		// subscribers see something. A production system would alert here.
	}
	if err := e.bus.Publish(ctx, ev); err != nil {
		log.Warn("publish event", "err", err)
	}
	return ev.Seq
}

// Emit kicks off execution for a freshly-created run. It returns
// immediately; the work continues on a goroutine.
//
// The request's context cannot be used here because the HTTP response
// flushes before the run is done. Phase 4 will introduce a per-run
// cancellation context so POST /runs/:id/cancel can reach this goroutine.
func (e *Executor) Emit(run domain.Run, tasks []domain.Task) {
	go e.execute(run, tasks)
}

func (e *Executor) execute(run domain.Run, tasks []domain.Task) {
	ctx, cancel := context.WithTimeout(context.Background(), e.runBudget)
	defer cancel()

	log := slog.With("run_id", run.ID)
	log.Info("run starting", "tasks", len(tasks))

	// Build a DAG to recover the validated invariants (topological order,
	// no cycles). RunService already constructed one at submit time; we
	// re-wrap here because Emit takes a flat list for now. If we ever pass
	// the original DAG through, we can skip this.
	dag, err := domain.NewDAG(run, tasks)
	if err != nil {
		log.Error("rebuild dag", "err", err)
		e.failRun(ctx, run.ID, fmt.Sprintf("rebuild dag: %v", err))
		return
	}

	now := time.Now().UTC()
	if err := e.store.MarkRunRunning(ctx, run.ID, now); err != nil {
		log.Error("mark run running", "err", err)
		return
	}
	e.emit(ctx, domain.NewEvent(run.ID, domain.EventRunStarted, nil))

	for _, task := range dag.TopologicalOrder() {
		if ctx.Err() != nil {
			log.Warn("run aborted", "err", ctx.Err())
			e.failRun(ctx, run.ID, ctx.Err().Error())
			return
		}
		if err := e.executeTask(ctx, run.ID, task); err != nil {
			log.Error("task failed", "task_id", task.ID, "err", err)
			e.failRun(ctx, run.ID, err.Error())
			return
		}
	}

	done := time.Now().UTC()
	if err := e.store.MarkRunCompleted(ctx, run.ID, done); err != nil {
		log.Error("mark run completed", "err", err)
		return
	}
	e.emit(ctx, domain.NewEvent(run.ID, domain.EventRunCompleted, nil))
	log.Info("run completed")
}

// executeTask is the per-task state machine: mark running + publish,
// execute the unit of work, mark completed + publish.
func (e *Executor) executeTask(ctx context.Context, runID uuid.UUID, task domain.Task) error {
	log := slog.With("run_id", runID, "task_id", task.ID, "kind", task.Kind)

	start := time.Now().UTC()
	if err := e.store.MarkTaskRunning(ctx, task.ID, start); err != nil {
		return fmt.Errorf("mark running: %w", err)
	}
	e.emit(ctx, domain.NewTaskEvent(runID, task.ID, domain.EventTaskStarted, nil))
	log.Info("task started")

	result, err := e.doWork(ctx, task)
	if err != nil {
		failAt := time.Now().UTC()
		if markErr := e.store.MarkTaskFailed(ctx, task.ID, err.Error(), failAt); markErr != nil {
			log.Error("mark task failed", "err", markErr)
		}
		payload, _ := json.Marshal(map[string]string{"error": err.Error()})
		e.emit(ctx, domain.NewTaskEvent(runID, task.ID, domain.EventTaskFailed, payload))
		return err
	}

	end := time.Now().UTC()
	if err := e.store.MarkTaskCompleted(ctx, task.ID, result, end); err != nil {
		return fmt.Errorf("mark completed: %w", err)
	}
	e.emit(ctx, domain.NewTaskEvent(runID, task.ID, domain.EventTaskCompleted, nil))
	log.Info("task completed")
	return nil
}

// doWork is the per-task work unit. Today: sleep + return a synthetic
// result. Phase 3 replaces this with a switch on task.Kind that dispatches
// to fetch / transform / ai workers.
func (e *Executor) doWork(ctx context.Context, task domain.Task) (json.RawMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(e.taskDuration):
	}
	result, _ := json.Marshal(map[string]any{
		"kind":    string(task.Kind),
		"elapsed": e.taskDuration.String(),
		"note":    "stub result — real execution arrives in Phase 3",
	})
	return result, nil
}

// failRun marks the run failed in the DB and publishes a run_failed event.
// Errors from the store are logged but not propagated — the caller is
// already in an error path.
func (e *Executor) failRun(ctx context.Context, runID uuid.UUID, reason string) {
	failedAt := time.Now().UTC()
	if err := e.store.MarkRunFailed(ctx, runID, failedAt); err != nil {
		slog.Error("mark run failed", "run_id", runID, "err", err)
	}
	payload, _ := json.Marshal(map[string]string{"reason": reason})
	e.emit(ctx, domain.NewEvent(runID, domain.EventRunFailed, payload))
}
