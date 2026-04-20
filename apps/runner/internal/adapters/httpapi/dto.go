package httpapi

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/itsPat/agent-runner/apps/runner/internal/app"
	"github.com/itsPat/agent-runner/apps/runner/internal/domain"
)

// These DTOs are the HTTP adapter's public contract. They live here, not
// in domain/, so that HTTP-shaped concerns (json tags, field renames,
// omitted zero values) never leak into the domain types.

type submitRunRequest struct {
	Goal string `json:"goal"`
}

type runDTO struct {
	ID          uuid.UUID  `json:"id"`
	Goal        string     `json:"goal"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

func runToDTO(r domain.Run) runDTO {
	return runDTO{
		ID:          r.ID,
		Goal:        r.Goal,
		Status:      string(r.Status),
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
		CompletedAt: r.CompletedAt,
	}
}

type taskDTO struct {
	ID          uuid.UUID       `json:"id"`
	RunID       uuid.UUID       `json:"run_id"`
	Kind        string          `json:"kind"`
	Spec        json.RawMessage `json:"spec"`
	DependsOn   []uuid.UUID     `json:"depends_on"`
	Status      string          `json:"status"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	Attempts    int             `json:"attempts"`
	CreatedAt   time.Time       `json:"created_at"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}

func taskToDTO(t domain.Task) taskDTO {
	return taskDTO{
		ID:          t.ID,
		RunID:       t.RunID,
		Kind:        string(t.Kind),
		Spec:        t.Spec,
		DependsOn:   t.DependsOn,
		Status:      string(t.Status),
		Result:      t.Result,
		Error:       t.Error,
		Attempts:    t.Attempts,
		CreatedAt:   t.CreatedAt,
		StartedAt:   t.StartedAt,
		CompletedAt: t.CompletedAt,
	}
}

type runDetailDTO struct {
	Run   runDTO    `json:"run"`
	Tasks []taskDTO `json:"tasks"`
}

func runDetailToDTO(d app.RunDetail) runDetailDTO {
	tasks := make([]taskDTO, len(d.Tasks))
	for i, t := range d.Tasks {
		tasks[i] = taskToDTO(t)
	}
	return runDetailDTO{
		Run:   runToDTO(d.Run),
		Tasks: tasks,
	}
}

type errorDTO struct {
	Error string `json:"error"`
}
