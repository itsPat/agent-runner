package ports

import (
	"context"

	"github.com/itsPat/agent-runner/apps/runner/internal/domain"
)

// AIPlanner decomposes a user goal into a validated DAG of tasks. The
// implementation is expected to call an external AI service; adapters
// translate between the transport representation (e.g. protobuf) and
// domain types.
//
// Returning domain.DAG (rather than a loose []Task) means the adapter
// has already done cycle + reference-integrity validation via NewDAG.
// Callers can persist the result without further checks.
type AIPlanner interface {
	PlanGoal(ctx context.Context, run domain.Run) (domain.DAG, error)
}
