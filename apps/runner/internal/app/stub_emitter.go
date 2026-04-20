package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/itsPat/agent-runner/apps/runner/internal/domain"
	"github.com/itsPat/agent-runner/apps/runner/internal/ports"
)

// StubEmitter fires synthetic task events through the EventBus to prove
// the pub/sub + SSE pipeline. It does not touch the database and does not
// respect task dependencies — that job belongs to the real executor in
// Phase 1.4.
type StubEmitter struct {
	bus      ports.EventBus
	tickGap  time.Duration
	runBudget time.Duration
}

// NewStubEmitter constructs an emitter with sensible defaults.
func NewStubEmitter(bus ports.EventBus) *StubEmitter {
	return &StubEmitter{
		bus:       bus,
		tickGap:   1500 * time.Millisecond,
		runBudget: 60 * time.Second,
	}
}

// Emit kicks off a background goroutine that walks tasks in the order
// given and publishes started/completed events for each, with tickGap
// between transitions. It returns immediately.
//
// The request's context cannot be used here because it dies when the HTTP
// response flushes. A fresh context with a generous timeout stands in
// until Phase 1.4 introduces per-run cancellation.
func (e *StubEmitter) Emit(run domain.Run, tasks []domain.Task) {
	go e.run(run, tasks)
}

func (e *StubEmitter) run(run domain.Run, tasks []domain.Task) {
	ctx, cancel := context.WithTimeout(context.Background(), e.runBudget)
	defer cancel()

	_ = e.bus.Publish(ctx, domain.NewEvent(run.ID, domain.EventRunStarted, nil))

	for _, t := range tasks {
		select {
		case <-ctx.Done():
			slog.Warn("stub emitter aborted", "run_id", run.ID, "err", ctx.Err())
			return
		case <-time.After(e.tickGap):
		}
		_ = e.bus.Publish(ctx, domain.NewTaskEvent(run.ID, t.ID, domain.EventTaskStarted, nil))

		select {
		case <-ctx.Done():
			return
		case <-time.After(e.tickGap):
		}
		_ = e.bus.Publish(ctx, domain.NewTaskEvent(run.ID, t.ID, domain.EventTaskCompleted, nil))
	}

	_ = e.bus.Publish(ctx, domain.NewEvent(run.ID, domain.EventRunCompleted, nil))
	slog.Info("stub emitter done", "run_id", run.ID, "tasks", len(tasks))
}
