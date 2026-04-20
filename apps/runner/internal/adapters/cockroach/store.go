package cockroach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itsPat/agent-runner/apps/runner/internal/domain"
	"github.com/itsPat/agent-runner/apps/runner/internal/ports"
)

// TaskStore is the pgx-backed implementation of ports.TaskStore.
type TaskStore struct {
	pool *pgxpool.Pool
}

// Compile-time assertion: *TaskStore satisfies ports.TaskStore. If we ever
// drift from the interface, this line will fail the build.
var _ ports.TaskStore = (*TaskStore)(nil)

func NewTaskStore(pool *pgxpool.Pool) *TaskStore {
	return &TaskStore{pool: pool}
}

// CreateRun atomically inserts the run and all of its tasks. pgx.BeginFunc
// commits on a nil return and rolls back on any error or panic.
func (s *TaskStore) CreateRun(ctx context.Context, dag domain.DAG) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO runs (id, goal, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5)
		`,
			dag.Run.ID, dag.Run.Goal, string(dag.Run.Status),
			dag.Run.CreatedAt, dag.Run.UpdatedAt,
		); err != nil {
			return fmt.Errorf("insert run: %w", err)
		}

		// Bulk insert would be faster via CopyFrom, but Phase 1 DAGs cap at
		// ~20 tasks — individual inserts are plenty and keep the code
		// readable. Swap to CopyFrom if profiling ever demands it.
		for _, t := range dag.Tasks {
			if _, err := tx.Exec(ctx, `
				INSERT INTO tasks (
					id, run_id, kind, spec, depends_on, status,
					attempts, created_at
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			`,
				t.ID, t.RunID, string(t.Kind), []byte(t.Spec),
				t.DependsOn, string(t.Status), t.Attempts, t.CreatedAt,
			); err != nil {
				return fmt.Errorf("insert task %s: %w", t.ID, err)
			}
		}
		return nil
	})
}

// runRow is the on-the-wire shape of a runs row. Lives in the adapter, not
// the domain — the domain.Run type is storage-agnostic.
type runRow struct {
	ID          uuid.UUID  `db:"id"`
	Goal        string     `db:"goal"`
	Status      string     `db:"status"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
	CompletedAt *time.Time `db:"completed_at"`
}

func (r runRow) toDomain() domain.Run {
	return domain.Run{
		ID:          r.ID,
		Goal:        r.Goal,
		Status:      domain.RunStatus(r.Status),
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
		CompletedAt: r.CompletedAt,
	}
}

func (s *TaskStore) GetRun(ctx context.Context, id uuid.UUID) (domain.Run, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, goal, status, created_at, updated_at, completed_at
		FROM runs
		WHERE id = $1
	`, id)
	if err != nil {
		return domain.Run{}, fmt.Errorf("query run: %w", err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[runRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Run{}, ports.ErrNotFound
		}
		return domain.Run{}, fmt.Errorf("scan run: %w", err)
	}
	return row.toDomain(), nil
}

type taskRow struct {
	ID          uuid.UUID       `db:"id"`
	RunID       uuid.UUID       `db:"run_id"`
	Kind        string          `db:"kind"`
	Spec        json.RawMessage `db:"spec"`
	DependsOn   []uuid.UUID     `db:"depends_on"`
	Status      string          `db:"status"`
	Result      json.RawMessage `db:"result"`
	Error       *string         `db:"error"`
	Attempts    int             `db:"attempts"`
	CreatedAt   time.Time       `db:"created_at"`
	StartedAt   *time.Time      `db:"started_at"`
	CompletedAt *time.Time      `db:"completed_at"`
}

func (r taskRow) toDomain() domain.Task {
	errStr := ""
	if r.Error != nil {
		errStr = *r.Error
	}
	return domain.Task{
		ID:          r.ID,
		RunID:       r.RunID,
		Kind:        domain.TaskKind(r.Kind),
		Spec:        r.Spec,
		DependsOn:   r.DependsOn,
		Status:      domain.TaskStatus(r.Status),
		Result:      r.Result,
		Error:       errStr,
		Attempts:    r.Attempts,
		CreatedAt:   r.CreatedAt,
		StartedAt:   r.StartedAt,
		CompletedAt: r.CompletedAt,
	}
}

// --- Status transitions ---
// Each method is a single UPDATE by primary key. We also update runs.updated_at
// opportunistically on run transitions so clients sorting by it see the
// correct "most recent change" ordering.

func (s *TaskStore) MarkRunRunning(ctx context.Context, runID uuid.UUID, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE runs SET status = 'running', updated_at = $2
		WHERE id = $1
	`, runID, at)
	if err != nil {
		return fmt.Errorf("mark run running: %w", err)
	}
	return nil
}

func (s *TaskStore) MarkRunCompleted(ctx context.Context, runID uuid.UUID, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE runs SET status = 'completed', updated_at = $2, completed_at = $2
		WHERE id = $1
	`, runID, at)
	if err != nil {
		return fmt.Errorf("mark run completed: %w", err)
	}
	return nil
}

func (s *TaskStore) MarkRunFailed(ctx context.Context, runID uuid.UUID, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE runs SET status = 'failed', updated_at = $2, completed_at = $2
		WHERE id = $1
	`, runID, at)
	if err != nil {
		return fmt.Errorf("mark run failed: %w", err)
	}
	return nil
}

func (s *TaskStore) MarkTaskRunning(ctx context.Context, taskID uuid.UUID, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE tasks SET status = 'running', started_at = $2, attempts = attempts + 1
		WHERE id = $1
	`, taskID, at)
	if err != nil {
		return fmt.Errorf("mark task running: %w", err)
	}
	return nil
}

func (s *TaskStore) MarkTaskCompleted(ctx context.Context, taskID uuid.UUID, result json.RawMessage, at time.Time) error {
	if result == nil {
		result = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE tasks SET status = 'completed', completed_at = $2, result = $3
		WHERE id = $1
	`, taskID, at, []byte(result))
	if err != nil {
		return fmt.Errorf("mark task completed: %w", err)
	}
	return nil
}

func (s *TaskStore) MarkTaskFailed(ctx context.Context, taskID uuid.UUID, errMsg string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE tasks SET status = 'failed', completed_at = $2, error = $3
		WHERE id = $1
	`, taskID, at, errMsg)
	if err != nil {
		return fmt.Errorf("mark task failed: %w", err)
	}
	return nil
}

func (s *TaskStore) ListTasks(ctx context.Context, runID uuid.UUID) ([]domain.Task, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, kind, spec, depends_on, status,
		       result, error, attempts, created_at, started_at, completed_at
		FROM tasks
		WHERE run_id = $1
		ORDER BY created_at ASC, id ASC
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	rs, err := pgx.CollectRows(rows, pgx.RowToStructByName[taskRow])
	if err != nil {
		return nil, fmt.Errorf("scan tasks: %w", err)
	}
	out := make([]domain.Task, len(rs))
	for i, r := range rs {
		out[i] = r.toDomain()
	}
	return out, nil
}
