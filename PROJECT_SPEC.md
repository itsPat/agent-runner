# Project Spec: Distributed AI Agent Runner

> **Status:** Design / pre-build
> **Purpose:** Source of truth for what we're building and why. All decisions live here.

---

## One-line description

A distributed task runner where users submit goals, an AI decomposes them into a DAG of subtasks, and a Go orchestrator executes the DAG across a pool of workers with retries, timeouts, and live progress streaming to a React frontend.

## Why this project

Chosen to deeply exercise **Go's concurrency model** while touching CockroachDB, ai-sdk (TypeScript), protobuf, TanStack Start, and shadcn. The project is useful enough to demo to someone, but the primary goal is stack learning — Go especially.

### What this project is *not*

- Not a production Temporal/Airflow replacement. It's a learning artifact.
- Not a general-purpose workflow engine. Scope is deliberately narrow.
- Not trying to handle edge cases a real system would (cluster coordination, cross-DC failover, etc.).

---

## The core loop

1. User submits a goal in natural language (e.g. "Research the top 3 EV battery companies and summarize their 2025 financials").
2. Go backend forwards the goal to the **AI Planner Service** (TypeScript + ai-sdk).
3. AI Planner returns a structured **task DAG** (nodes = tasks, edges = dependencies) via protobuf.
4. Go **Orchestrator** persists the DAG to Cockroach and begins execution.
5. A pool of **Go Workers** pulls ready tasks from the DAG (tasks whose dependencies are complete).
6. Each task is one of:
   - **AI task** — sent to the AI service for structured execution (summarize, extract, classify, etc.).
   - **Fetch task** — Go makes an HTTP call (web fetch, API call).
   - **Transform task** — pure Go (merge, filter, format results from prior tasks).
7. Task results are persisted to Cockroach. Completion unblocks dependent tasks.
8. The React frontend subscribes to a live SSE or WebSocket stream and renders the DAG updating in real time.
9. When all tasks complete (or the DAG fails), the user gets a final assembled result.

---

## Architecture

### Services

| Service | Language | Responsibility |
|---|---|---|
| **Orchestrator** | Go | DAG execution, worker coordination, state management, client-facing API |
| **AI Planner/Executor** | TypeScript (ai-sdk) | Goal decomposition, AI task execution, structured output |
| **Frontend** | React (TanStack Start + shadcn) | Submit goals, watch DAGs execute live, view results |
| **Database** | CockroachDB | Persistent state: runs, tasks, task results, events |

### Communication

- **Frontend ↔ Orchestrator:** HTTP for commands, SSE (or WebSocket) for live DAG updates
- **Orchestrator ↔ AI Service:** gRPC with protobuf (this is where protobuf earns its keep)
- **Orchestrator ↔ Cockroach:** standard SQL via `pgx`

### Why each piece is here

- **Go orchestrator:** the heart of the project. Worker pool, context cancellation, channel-based coordination, graceful shutdown, retries with backoff — this is idiomatic Go territory.
- **Cockroach:** task queue pattern with `SELECT ... FOR UPDATE SKIP LOCKED`, transactional state transitions when a task completes and unblocks dependents. Honest note: we could do this on Postgres. We're using Cockroach for exposure.
- **AI service in TS:** ai-sdk has the best DX for structured outputs, streaming, and tool calls. Isolating it as its own service makes the protobuf boundary meaningful.
- **Protobuf:** the Orchestrator ↔ AI service contract is well-typed and versionable. Task definitions, results, and DAG structures are all structured data — perfect fit.
- **TanStack Start + shadcn:** modern React, file-based routing, good SSR story, shadcn for clean UI without designing from scratch.

---

## What makes Go shine here

- **Worker pool with bounded concurrency** — classic `chan Task` + N goroutines pattern.
- **Context propagation** — user cancels a run, cancellation flows through orchestrator → worker → in-flight gRPC call to AI service.
- **Timeouts per task** — each task runs with its own `context.WithTimeout`.
- **Retries with backoff** — goroutine retry loops with jittered exponential backoff.
- **Fan-out/fan-in** — DAG execution is literally the fan-out/fan-in pattern.
- **SSE streaming to frontend** — goroutine per connected client, event channel fan-out.
- **Graceful shutdown** — SIGTERM → stop accepting new work → drain in-flight tasks → close DB pool.
- **`select` statements** — choosing between "new task ready," "task finished," "context cancelled," "shutdown signal."

---

## Data model (rough)

```
runs
  id (uuid)
  goal (text)
  status (pending | running | completed | failed | cancelled)
  created_at, updated_at, completed_at

tasks
  id (uuid)
  run_id (fk → runs)
  kind (ai | fetch | transform)
  spec (jsonb)         -- task-specific params
  depends_on (uuid[])  -- task ids this task depends on
  status (pending | ready | running | completed | failed)
  result (jsonb, nullable)
  error (text, nullable)
  attempts (int)
  created_at, started_at, completed_at

events
  id (uuid)
  run_id (fk → runs)
  task_id (fk → tasks, nullable)
  kind (task_started | task_completed | task_failed | run_completed | ...)
  payload (jsonb)
  created_at
```

Events table drives the SSE stream. Frontend subscribes to events for a run_id and updates the UI.

---

## Protobuf boundary (Orchestrator ↔ AI service)

Three RPCs, roughly:

```proto
service AIService {
  // Given a goal, return a task DAG.
  rpc PlanGoal(PlanGoalRequest) returns (PlanGoalResponse);

  // Execute a single AI task with upstream results as context.
  rpc ExecuteTask(ExecuteTaskRequest) returns (stream ExecuteTaskEvent);

  // Summarize final DAG results into a user-facing answer.
  rpc Summarize(SummarizeRequest) returns (stream SummarizeEvent);
}
```

Streaming responses from the AI service let the frontend see tokens arrive live. Go forwards the stream through to the SSE connection.

---

## Scope guardrails

To keep this finishable:

- **Max ~20 tasks per DAG.** Planner prompt enforces this.
- **Three task kinds only.** ai, fetch, transform. No shell exec, no arbitrary code.
- **Single-node orchestrator.** No leader election, no multi-node coordination.
- **Single AI service instance.** No sharding, no load balancing.
- **Local Cockroach in Docker.** Single node, no cluster setup.
- **No auth for v1.** Single-user tool running locally.
- **No persistent connections pooling layer.** Just pgx defaults.

---

## Explicit non-goals

- Horizontal scaling of orchestrator
- Multi-tenancy / auth / user accounts
- Arbitrary code execution in tasks (security nightmare, wrong scope)
- Rich task library beyond the three kinds
- Sophisticated planning (no ReAct loops, no reflection, no replanning mid-run — v1 is plan once, execute)
- Cost tracking / token accounting
- Persistent task queues across orchestrator restarts (v1: crashed runs stay crashed)

---

## Resolved decisions

- **Orchestrator ↔ AI service:** gRPC (full gRPC, not Connect/Twirp).
- **Frontend transport:** SSE for server→client updates.
- **Protobuf codegen:** `buf` (handles plugin versions, linting, breaking-change detection).
- **Local dev:** Docker Compose for Cockroach + AI service + Go orchestrator.
- **Crashed run recovery:** out of scope for v1. Stretch goal (see below). v1 behavior: runs in-flight when the orchestrator dies stay stuck in `running` — acceptable.

## Stretch goals (post-v1)

- **Crashed run recovery.** On orchestrator start, find runs/tasks in `running` state and either resume execution or mark them `failed` with reason `orchestrator_restart`. Requires task heartbeats or a startup reconciliation sweep.
- **Replanning on failure.** Let the AI planner revise the DAG when a task fails irrecoverably.
- **Cost/token tracking** per run.
- **Multiple concurrent runs** with fair scheduling across the worker pool.

## Open questions to resolve during build

1. **Task result size limits?** Need to cap or paginate in Cockroach to avoid huge jsonb blobs.

---

## Success criteria (when is v1 "done"?)

- Submit a multi-step goal from the frontend.
- See the DAG render and update live as tasks complete.
- At least one AI task, one fetch task, one transform task in the demo DAG.
- Cancellation works: hitting "cancel" mid-run stops in-flight work within a few seconds.
- One task failure retries with backoff, then fails the run cleanly if retries exhausted.
- Restart the orchestrator mid-run and see it either resume or mark the run as failed (whichever we decide).
- A short README with a one-command local bring-up.
