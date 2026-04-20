// Package app holds the runner's use cases. Each method on RunService
// fulfills one user intent. The service depends only on domain and ports;
// adapters and transports are never imported from here.
package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/itsPat/agent-runner/apps/runner/internal/domain"
	"github.com/itsPat/agent-runner/apps/runner/internal/ports"
)

// RunService is the orchestration layer for run-related use cases.
type RunService struct {
	store    ports.TaskStore
	planner  ports.AIPlanner
	executor *Executor
}

func NewRunService(store ports.TaskStore, planner ports.AIPlanner, executor *Executor) *RunService {
	return &RunService{store: store, planner: planner, executor: executor}
}

// SubmitGoal plans the goal into a DAG via the AIPlanner port, persists
// it, and kicks off execution. Returns the Run immediately — planning
// runs inside this call (it's fast enough to sit on the hot path) but
// execution happens on a background goroutine.
func (s *RunService) SubmitGoal(ctx context.Context, goal string) (domain.Run, error) {
	if goal == "" {
		return domain.Run{}, fmt.Errorf("goal is required")
	}

	run := domain.NewRun(goal)

	// The planner returns an already-validated DAG (cycle-free,
	// reference-integrity). We do not re-call NewDAG here.
	dag, err := s.planner.PlanGoal(ctx, run)
	if err != nil {
		return domain.Run{}, fmt.Errorf("plan goal: %w", err)
	}

	if err := s.store.CreateRun(ctx, dag); err != nil {
		return domain.Run{}, fmt.Errorf("persist run: %w", err)
	}

	// Kick off background execution. Emit returns immediately.
	s.executor.Emit(dag.Run, dag.Tasks)
	return run, nil
}

// RunDetail bundles a run with its tasks for the run-detail view.
type RunDetail struct {
	Run   domain.Run
	Tasks []domain.Task
}

// GetRunDetail loads a run and its tasks. Returns ports.ErrNotFound when
// the run id does not exist.
func (s *RunService) GetRunDetail(ctx context.Context, id uuid.UUID) (RunDetail, error) {
	run, err := s.store.GetRun(ctx, id)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return RunDetail{}, ports.ErrNotFound
		}
		return RunDetail{}, fmt.Errorf("load run: %w", err)
	}
	tasks, err := s.store.ListTasks(ctx, id)
	if err != nil {
		return RunDetail{}, fmt.Errorf("list tasks: %w", err)
	}
	return RunDetail{Run: run, Tasks: tasks}, nil
}

