package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var (
	ErrEmptyDAG      = errors.New("dag has no tasks")
	ErrUnknownDep    = errors.New("task depends on unknown task id")
	ErrCycleDetected = errors.New("dag has a cycle")
)

// DAG is a validated set of Tasks for a single Run. The named type carries
// the invariant "no cycles, no dangling dependency references." Build one
// with NewDAG; never assemble a DAG literal by hand.
type DAG struct {
	Run   Run
	Tasks []Task
}

// NewDAG validates the task graph and returns a DAG on success. It rejects
// empty graphs, edges pointing to non-existent tasks, and cycles.
func NewDAG(run Run, tasks []Task) (DAG, error) {
	if len(tasks) == 0 {
		return DAG{}, ErrEmptyDAG
	}

	ids := make(map[uuid.UUID]int, len(tasks))
	for i, t := range tasks {
		ids[t.ID] = i
	}
	for _, t := range tasks {
		for _, dep := range t.DependsOn {
			if _, ok := ids[dep]; !ok {
				return DAG{}, fmt.Errorf("%w: task %s -> %s", ErrUnknownDep, t.ID, dep)
			}
		}
	}

	// Classic 3-color DFS cycle check.
	// white = unvisited, gray = on the current path, black = fully explored.
	// Seeing a gray node on an outgoing edge means we've looped back.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[uuid.UUID]int, len(tasks))
	var visit func(id uuid.UUID) error
	visit = func(id uuid.UUID) error {
		switch color[id] {
		case gray:
			return ErrCycleDetected
		case black:
			return nil
		}
		color[id] = gray
		for _, dep := range tasks[ids[id]].DependsOn {
			if err := visit(dep); err != nil {
				return err
			}
		}
		color[id] = black
		return nil
	}
	for _, t := range tasks {
		if err := visit(t.ID); err != nil {
			return DAG{}, err
		}
	}

	return DAG{Run: run, Tasks: tasks}, nil
}
