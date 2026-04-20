// Package app holds the runner's use cases. Each method on RunService
// fulfills one user intent. The service depends only on domain and ports;
// adapters and transports are never imported from here.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/itsPat/agent-runner/apps/runner/internal/domain"
	"github.com/itsPat/agent-runner/apps/runner/internal/ports"
)

// RunService is the orchestration layer for run-related use cases.
type RunService struct {
	store ports.TaskStore
}

func NewRunService(store ports.TaskStore) *RunService {
	return &RunService{store: store}
}

// SubmitGoal persists a new Run with a hardcoded 2-task DAG. In Phase 2 the
// hardcoded planner is replaced by a real AIPlanner port; this method's
// signature does not change.
func (s *RunService) SubmitGoal(ctx context.Context, goal string) (domain.Run, error) {
	if goal == "" {
		return domain.Run{}, fmt.Errorf("goal is required")
	}

	run := domain.NewRun(goal)
	tasks := stubDAGForGoal(run, goal)

	dag, err := domain.NewDAG(run, tasks)
	if err != nil {
		return domain.Run{}, fmt.Errorf("build dag: %w", err)
	}
	if err := s.store.CreateRun(ctx, dag); err != nil {
		return domain.Run{}, fmt.Errorf("persist run: %w", err)
	}
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

// stubDAGForGoal returns a deterministic 2-task DAG: fetch -> transform.
// This is Phase 1 scaffolding. Phase 2 replaces it with the AI planner.
func stubDAGForGoal(run domain.Run, goal string) []domain.Task {
	fetchSpec, _ := json.Marshal(map[string]string{"goal": goal})
	fetch := domain.NewTask(run.ID, domain.TaskKindFetch, fetchSpec, nil)

	transformSpec, _ := json.Marshal(map[string]string{"op": "summarize"})
	transform := domain.NewTask(run.ID, domain.TaskKindTransform, transformSpec,
		[]uuid.UUID{fetch.ID})

	return []domain.Task{fetch, transform}
}
