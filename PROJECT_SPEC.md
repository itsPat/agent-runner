# Project Spec: Distributed AI Agent Runner

> **Status:** Phase 0 complete. Repo layout, ConnectRPC wiring, and health-check flow are in place.
> **Purpose:** Source of truth for what we're building and why. All decisions live here.

---

## One-line description

A distributed task runner where users submit goals, an AI decomposes them into a DAG of subtasks, and a Go runner service executes the DAG across a pool of workers with retries, timeouts, and live progress streaming to a React web app.

## Why this project

Chosen to deeply exercise **Go's concurrency model** while touching CockroachDB, ai-sdk (TypeScript), protobuf, ConnectRPC, TanStack Start, and shadcn. The project is useful enough to demo to someone, but the primary goal is stack learning — Go especially.

### What this project is *not*

- Not a production Temporal/Airflow replacement. It's a learning artifact.
- Not a general-purpose workflow engine. Scope is deliberately narrow.
- Not trying to handle edge cases a real system would (cluster coordination, cross-DC failover, etc.).

---

## The core loop

1. User submits a goal in natural language (e.g. "Research the top 3 EV battery companies and summarize their 2025 financials").
2. Go runner forwards the goal to the **AI Service** (TypeScript + ai-sdk, under `apps/ai`).
3. AI Service returns a structured **task DAG** (nodes = tasks, edges = dependencies) via protobuf.
4. Go runner persists the DAG to Cockroach and begins execution.
5. A pool of **Go workers** (inside the runner) pulls ready tasks from the DAG (tasks whose dependencies are complete).
6. Each task is one of:
   - **AI task** — sent to the AI service for structured execution (summarize, extract, classify, etc.).
   - **Fetch task** — Go makes an HTTP call (web fetch, API call).
   - **Transform task** — pure Go (merge, filter, format results from prior tasks).
7. Task results are persisted to Cockroach. Completion unblocks dependent tasks.
8. The React web app subscribes to a live SSE stream and renders the DAG updating in real time.
9. When all tasks complete (or the DAG fails), the user gets a final assembled result.

---

## Architecture

### Services

| Component | Language | Responsibility |
|---|---|---|
| **Runner** (`apps/runner/`) | Go | Core domain: DAG execution, worker pool, state management, client-facing API. Built in hexagonal style. |
| **AI Service** (`apps/ai/`) | TypeScript (ai-sdk, bun runtime) | Goal decomposition, AI task execution, structured output |
| **Web App** (`apps/web/`) | React (TanStack Start + shadcn) | Submit goals, watch DAGs execute live, view results |
| **Database** | CockroachDB | Persistent state: runs, tasks, task results, events |

Future deployables live under `apps/`. Shared code can move into `packages/` later.

### Communication

- **Web App ↔ Runner:** HTTP for commands, SSE for live DAG updates
- **Runner ↔ AI Service:** ConnectRPC with protobuf (this is where protobuf earns its keep)
- **Runner ↔ Cockroach:** standard SQL via `pgx`

### Architectural style: hexagonal (ports and adapters)

The Go runner follows **hexagonal architecture**. Domain and use-case code (`domain/`, `app/`) depend only on **port interfaces** (`ports/`). Concrete implementations live in `adapters/` and plug in from the outside at composition time (`cmd/server/main.go`).

```
apps/runner/
├── cmd/server/                 # composition root — wires adapters to ports
├── internal/
│   ├── domain/                 # entities: Run, Task, DAG. Pure Go, zero deps.
│   ├── app/                    # use cases: "plan goal", "execute DAG", worker pool
│   ├── ports/                  # interfaces the app needs from the outside
│   │   ├── aiplanner.go        # PlanGoal(goal) -> DAG
│   │   ├── taskstore.go        # SaveRun, LoadTask, etc.
│   │   └── eventbus.go         # Publish event
│   └── adapters/               # concrete implementations of ports
│       ├── connectai/          # calls apps/ai over ConnectRPC
│       ├── cockroach/          # pgx-backed task store
│       ├── httpapi/            # inbound HTTP/SSE handlers
│       └── memeventbus/        # in-memory pub/sub for SSE fan-out
```

**The rule:** `domain/` and `app/` never import from `adapters/`. They depend only on `ports/`. This keeps the core swappable and testable — the AI adapter can be replaced with a fake that returns a hardcoded DAG, Cockroach can be swapped for SQLite, etc.

### Why each piece is here

- **Go runner service (hexagonal).** The heart of the project. Worker pool, context cancellation, channel-based coordination, graceful shutdown, retries with backoff — idiomatic Go territory. Hexagonal layout keeps the learning deliberate: you practice separating domain logic from infra concerns.
- **Cockroach.** Task queue pattern with `SELECT ... FOR UPDATE SKIP LOCKED`, transactional state transitions when a task completes and unblocks dependents. Honest note: we could do this on Postgres. We're using Cockroach for exposure.
- **AI service in TS (bun).** ai-sdk has the best DX for structured outputs, streaming, and tool calls. Isolating it as its own service makes the protobuf boundary meaningful.
- **Protobuf/ConnectRPC.** The runner ↔ AI service contract is well-typed and versionable. Task definitions, results, and DAG structures are all structured data — perfect fit.
- **TanStack Start + shadcn.** Modern React, file-based routing, good SSR story, shadcn for clean UI without designing from scratch.

---

## What makes Go shine here

- **Worker pool with bounded concurrency** — classic `chan Task` + N goroutines pattern.
- **Context propagation** — user cancels a run, cancellation flows through app layer → worker → in-flight Connect call to AI service.
- **Timeouts per task** — each task runs with its own `context.WithTimeout`.
- **Retries with backoff** — goroutine retry loops with jittered exponential backoff.
- **Fan-out/fan-in** — DAG execution is literally the fan-out/fan-in pattern.
- **SSE streaming to the web app** — goroutine per connected client, event channel fan-out.
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

Events table drives the SSE stream. The web app subscribes to events for a run_id and updates the UI.

---

## Protobuf boundary (Runner ↔ AI service)

Three RPCs, roughly:

```proto
service AgentService {
  // Given a goal, return a task DAG.
  rpc PlanGoal(PlanGoalRequest) returns (PlanGoalResponse);

  // Execute a single AI task with upstream results as context.
  rpc ExecuteTask(ExecuteTaskRequest) returns (stream ExecuteTaskEvent);

  // Summarize final DAG results into a user-facing answer.
  rpc Summarize(SummarizeRequest) returns (stream SummarizeEvent);
}
```

Streaming responses from the AI service let the web app see tokens arrive live. The runner forwards the stream through to the SSE connection.

---

## Scope guardrails

To keep this finishable:

- **Max ~20 tasks per DAG.** Planner prompt enforces this.
- **Three task kinds only.** ai, fetch, transform. No shell exec, no arbitrary code.
- **Single-node runner.** No leader election, no multi-node coordination.
- **Single AI service instance.** No sharding, no load balancing.
- **Local Cockroach in Docker.** Single node, no cluster setup.
- **No auth for v1.** Single-user tool running locally.
- **No persistent connection pooling layer.** Just pgx defaults.

---

## Explicit non-goals

- Horizontal scaling of the runner
- Multi-tenancy / auth / user accounts
- Arbitrary code execution in tasks (security nightmare, wrong scope)
- Rich task library beyond the three kinds
- Sophisticated planning (no ReAct loops, no reflection, no replanning mid-run — v1 is plan once, execute)
- Cost tracking / token accounting
- Persistent task queues across runner restarts (v1: crashed runs stay crashed)

---

## Resolved decisions

- **Runner ↔ AI service:** ConnectRPC over HTTP.
- **Web app transport:** SSE for server→client updates.
- **Protobuf codegen:** `buf` (handles plugin versions, linting, breaking-change detection).
- **Local dev:** Docker Compose for Cockroach + runner + AI service.
- **Go runner architecture:** hexagonal (ports and adapters).
- **Crashed run recovery:** out of scope for v1. Stretch goal (see below). v1 behavior: runs in-flight when the runner dies stay stuck in `running` — acceptable.

## Stretch goals (post-v1)

- **Crashed run recovery.** On runner start, find runs/tasks in `running` state and either resume execution or mark them `failed` with reason `runner_restart`. Requires task heartbeats or a startup reconciliation sweep.
- **Replanning on failure.** Let the AI planner revise the DAG when a task fails irrecoverably.
- **Cost/token tracking** per run.
- **Multiple concurrent runs** with fair scheduling across the worker pool.

## Open questions to resolve during build

1. **Task result size limits?** Need to cap or paginate in Cockroach to avoid huge jsonb blobs.

---

## Success criteria (when is v1 "done"?)

- Submit a multi-step goal from the web app.
- See the DAG render and update live as tasks complete.
- At least one AI task, one fetch task, one transform task in the demo DAG.
- Cancellation works: hitting "cancel" mid-run stops in-flight work within a few seconds.
- One task failure retries with backoff, then fails the run cleanly if retries exhausted.
- Restart the runner mid-run and see it either resume or mark the run as failed (whichever we decide).
- A short README with a one-command local bring-up.
