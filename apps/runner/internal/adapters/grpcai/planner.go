// Package grpcai is the outbound adapter that calls the AI service over
// ConnectRPC (gRPC-compatible HTTP). It translates between protobuf
// request/response types and the runner's domain types. The app layer
// depends on ports.AIPlanner, not on this package.
package grpcai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/itsPat/agent-runner/apps/runner/internal/domain"
	"github.com/itsPat/agent-runner/apps/runner/internal/ports"
	agentv1 "github.com/itsPat/agent-runner/gen/go/agent/v1"
	"github.com/itsPat/agent-runner/gen/go/agent/v1/agentv1connect"
)

// Planner is the ConnectRPC-backed implementation of ports.AIPlanner.
type Planner struct {
	client agentv1connect.AgentServiceClient
}

// Compile-time assertion.
var _ ports.AIPlanner = (*Planner)(nil)

func NewPlanner(client agentv1connect.AgentServiceClient) *Planner {
	return &Planner{client: client}
}

// PlanGoal calls the AI service and returns a validated DAG. The method
// translates PlannedTask names (LLM-chosen strings) into domain UUIDs
// and rejects any DAG the domain layer wouldn't accept.
func (p *Planner) PlanGoal(ctx context.Context, run domain.Run) (domain.DAG, error) {
	resp, err := p.client.PlanGoal(ctx, &agentv1.PlanGoalRequest{Goal: run.Goal})
	if err != nil {
		return domain.DAG{}, fmt.Errorf("plan goal rpc: %w", err)
	}
	if len(resp.Tasks) == 0 {
		return domain.DAG{}, fmt.Errorf("planner returned zero tasks")
	}

	// Pass 1: assign a UUID to every planned task name. We allocate IDs
	// up front so Pass 2 can resolve cross-task references without
	// worrying about order.
	ids := make(map[string]uuid.UUID, len(resp.Tasks))
	for _, pt := range resp.Tasks {
		if pt.Name == "" {
			return domain.DAG{}, fmt.Errorf("planned task missing name")
		}
		if _, dup := ids[pt.Name]; dup {
			return domain.DAG{}, fmt.Errorf("duplicate planned task name: %q", pt.Name)
		}
		ids[pt.Name] = uuid.New()
	}

	// Pass 2: translate each PlannedTask into a domain.Task, resolving
	// names in depends_on against the map we just built.
	tasks := make([]domain.Task, 0, len(resp.Tasks))
	for _, pt := range resp.Tasks {
		kind, err := parseKind(pt.Kind)
		if err != nil {
			return domain.DAG{}, fmt.Errorf("task %q: %w", pt.Name, err)
		}
		spec, err := parseSpec(pt.SpecJson)
		if err != nil {
			return domain.DAG{}, fmt.Errorf("task %q: %w", pt.Name, err)
		}
		deps := make([]uuid.UUID, 0, len(pt.DependsOn))
		for _, depName := range pt.DependsOn {
			depID, ok := ids[depName]
			if !ok {
				return domain.DAG{}, fmt.Errorf("task %q depends on unknown task %q", pt.Name, depName)
			}
			deps = append(deps, depID)
		}
		task := domain.NewTask(run.ID, kind, spec, deps)
		task.ID = ids[pt.Name] // override the generated ID with the pre-assigned one
		tasks = append(tasks, task)
	}

	// Final gate: NewDAG enforces cycle-freeness and (redundantly)
	// reference integrity. The LLM's outputs can be creative; we don't
	// persist anything the domain wouldn't accept.
	dag, err := domain.NewDAG(run, tasks)
	if err != nil {
		return domain.DAG{}, fmt.Errorf("validate dag: %w", err)
	}
	return dag, nil
}

func parseKind(s string) (domain.TaskKind, error) {
	switch domain.TaskKind(s) {
	case domain.TaskKindAI, domain.TaskKindFetch, domain.TaskKindTransform:
		return domain.TaskKind(s), nil
	default:
		return "", fmt.Errorf("unknown kind %q", s)
	}
}

// parseSpec accepts either "" or a valid JSON document. Empty string
// becomes {} — the planner may omit spec for kinds that don't need it.
func parseSpec(s string) (json.RawMessage, error) {
	if s == "" {
		return json.RawMessage(`{}`), nil
	}
	var tmp any
	if err := json.Unmarshal([]byte(s), &tmp); err != nil {
		return nil, fmt.Errorf("spec is not valid json: %w", err)
	}
	return json.RawMessage(s), nil
}
