# agent-runner

A distributed AI agent runner. You give it a goal in natural language; an AI decomposes it into a DAG of subtasks; a Go backend executes the DAG across a worker pool with retries, timeouts, and live progress streaming to a React frontend.

## Why this exists

This is a learning project designed to deeply exercise Go's concurrency model while also getting hands-on with CockroachDB, protobuf/gRPC, ai-sdk (TypeScript), TanStack Start, and shadcn.

The project is useful enough to demo, but the primary goal is **stack fluency**, not shipping a product. Each design decision prioritizes learning over the shortest path.

### What it is not

- Not a production Temporal / Airflow / Inngest replacement.
- Not a general-purpose workflow engine.
- Not trying to handle the hard distributed systems edge cases (cluster coordination, cross-region failover, etc.).

## The core loop

1. User submits a goal in natural language (e.g. *"Research the top 3 EV battery companies and summarize their 2025 financials"*).
2. The Go backend forwards the goal to the AI Service (TypeScript + ai-sdk).
3. The AI Service returns a structured task DAG over gRPC (nodes = tasks, edges = dependencies).
4. The backend persists the DAG to CockroachDB and begins execution.
5. A pool of Go workers pulls ready tasks (dependencies satisfied) and executes them.
6. Each task is one of three kinds:
   - **AI task** — sent to the AI service for structured execution (summarize, extract, classify).
   - **Fetch task** — Go makes an HTTP call.
   - **Transform task** — pure Go (merge, filter, format results from upstream tasks).
7. Task results are persisted. Completion unblocks dependent tasks.
8. The React frontend subscribes to a live SSE stream and renders the DAG updating in real time.
9. When the DAG finishes, the user gets an assembled answer.

## Architecture

```
┌──────────────┐   HTTP + SSE    ┌────────────────┐   gRPC (proto)   ┌──────────────┐
│   Frontend   │ ───────────────▶│    Backend     │ ────────────────▶│  AI Service  │
│ TanStack +   │◀─────────────── │ (Go, hexagonal)│◀──────────────── │  (TS / bun)  │
│    shadcn    │                 └───────┬────────┘                  └──────────────┘
└──────────────┘                         │
                                         │ SQL (pgx)
                                         ▼
                                  ┌──────────────┐
                                  │ CockroachDB  │
                                  └──────────────┘
```

### Components

| Component | Language | Responsibility |
|---|---|---|
| **Backend** (`backend/`) | Go | Core domain. DAG execution, worker pool, state management, client API, SSE. Hexagonal architecture. |
| **AI Service** (`services/ai/`) | TypeScript (ai-sdk, bun) | Goal decomposition, AI task execution, structured output |
| **Frontend** (`frontend/`) | React (TanStack Start + shadcn) | Submit goals, watch DAGs execute live, view results |
| **Database** | CockroachDB | Persistent state: runs, tasks, events |

Future satellite services (scrapers, notifiers, etc.) live alongside the AI service under `services/`.

### Communication

- **Frontend ↔ Backend:** HTTP for commands, SSE for live DAG updates.
- **Backend ↔ AI Service:** gRPC with protobuf. The protobuf contract lives in `proto/`.
- **Backend ↔ Cockroach:** standard SQL via `pgx`.

### Backend architecture (hexagonal)

The Go backend follows **hexagonal architecture** (ports and adapters). Domain and use-case code depends only on port interfaces; concrete implementations plug in from the outside.

```
backend/
├── cmd/server/          # composition root — wires adapters to ports
└── internal/
    ├── domain/          # entities: Run, Task, DAG. Pure Go, zero deps.
    ├── app/             # use cases: planning, DAG execution, worker pool
    ├── ports/           # interfaces the app needs from the outside
    │   ├── aiplanner.go     # PlanGoal(goal) -> DAG
    │   ├── taskstore.go     # SaveRun, LoadTask, etc.
    │   └── eventbus.go      # Publish event
    └── adapters/        # concrete implementations of ports
        ├── grpcai/          # calls services/ai over gRPC
        ├── cockroach/       # pgx-backed task store
        ├── httpapi/         # inbound HTTP/SSE handlers
        └── memeventbus/     # in-memory pub/sub for SSE fan-out
```

**The rule:** `domain/` and `app/` never import from `adapters/`. They depend only on `ports/`. This keeps the core swappable and testable.

## Why each technology

- **Go backend.** The heart of the project. Worker pool, context cancellation, channel-based coordination, graceful shutdown, retries with backoff — textbook Go.
- **Hexagonal architecture.** Deliberate practice of separating domain logic from infra concerns. Makes the AI service mockable with a fake adapter, lets us swap Cockroach later if we want.
- **CockroachDB.** Used here mostly for exposure, but the task queue pattern (`SELECT ... FOR UPDATE SKIP LOCKED`) and transactional state transitions are real. Honest note: Postgres would work just as well for v1 scale.
- **AI service in TypeScript.** `ai-sdk` has the best DX for structured outputs, streaming, and tool calls. Isolating it as its own service makes the protobuf boundary meaningful.
- **Protobuf / gRPC.** The backend ↔ AI service contract is well-typed and versionable. Task definitions, results, and DAG structures are all structured data — a perfect fit.
- **TanStack Start + shadcn.** Modern React with file-based routing, a good SSR story, and shadcn for clean UI without designing from scratch.
- **bun.** Faster than Node for the TS pieces, single-binary toolchain (runtime + package manager + bundler).

## What makes Go shine here

This project was chosen specifically to exercise these patterns:

- **Worker pool with bounded concurrency** — `chan Task` + N goroutines.
- **Context propagation** — user cancels a run; cancellation flows through app layer → worker → in-flight gRPC call to AI service.
- **Per-task timeouts** — `context.WithTimeout` per task.
- **Retries with backoff** — jittered exponential backoff in goroutine retry loops.
- **Fan-out/fan-in** — DAG execution is literally this pattern.
- **SSE streaming** — goroutine per connected client, event channel fan-out.
- **Graceful shutdown** — SIGTERM → stop new work → drain in-flight → close DB pool.
- **`select` statements** — multiplexing channels for "new task ready," "task finished," "context cancelled," "shutdown signal."

## Repository layout

```
agent-runner/
├── proto/              # .proto files + buf config (source of truth for RPC)
│   ├── buf.yaml
│   ├── buf.gen.yaml
│   └── agent/v1/
│       └── service.proto
├── gen/                # generated code — DO NOT edit by hand
│   ├── go/
│   └── ts/
├── backend/            # Go core (hexagonal)
│   ├── cmd/server/
│   ├── internal/
│   │   ├── domain/
│   │   ├── app/
│   │   ├── ports/
│   │   └── adapters/
│   ├── go.mod
│   └── Dockerfile
├── services/           # non-Go satellite services
│   └── ai/             # TS + ai-sdk (bun runtime)
│       ├── src/
│       ├── package.json
│       └── Dockerfile
├── frontend/           # TanStack Start + shadcn
│   ├── src/
│   └── package.json
├── docker-compose.yml  # cockroach + backend + ai service
├── Makefile            # top-level commands
├── PROJECT_SPEC.md     # what we're building and why
├── ROADMAP.md          # build plan, phase by phase
└── README.md
```

### Why this layout

- Each subproject owns its own toolchain. Go has `go.mod`; TS projects have `package.json`. No single tool pretends to manage everything.
- `proto/` is the contract boundary. Both Go and TS consume it. Changes to `.proto` files propagate via `make proto`.
- `services/` is where non-Go satellite services live. Today it's just `ai`; tomorrow it could host scrapers, notifiers, or anything else better suited to a different language.
- `gen/` is committed so fresh clones work without first installing buf. Revisit if diffs get noisy.

## Data model

```
runs
  id, goal, status, timestamps

tasks
  id, run_id, kind (ai|fetch|transform), spec (jsonb), depends_on (uuid[]),
  status, result (jsonb), error, attempts, timestamps

events
  id, run_id, task_id, kind, payload (jsonb), created_at
```

The `events` table drives the SSE stream. The frontend subscribes per `run_id` and updates the UI as events arrive.

## Protobuf boundary

```proto
service AgentService {
  rpc PlanGoal(PlanGoalRequest) returns (PlanGoalResponse);
  rpc ExecuteTask(ExecuteTaskRequest) returns (stream ExecuteTaskEvent);
  rpc Summarize(SummarizeRequest) returns (stream SummarizeEvent);
}
```

Streaming responses let the frontend see AI tokens arrive live — the backend forwards the gRPC stream into the SSE connection.

## Prerequisites

- **Go** 1.22+
- **Bun** 1.1+
- **Docker** + Docker Compose
- **buf** CLI (`brew install bufbuild/buf/buf`)

## Quick start

```bash
# One-time setup — install deps across all subprojects
make setup

# Generate protobuf code (run whenever .proto files change)
make proto

# Bring up cockroach, backend, ai service
make up

# Frontend runs outside compose for hot reload
cd frontend && bun dev
```

Then open http://localhost:3000.

### Common commands

| Command | What it does |
|---|---|
| `make setup` | Install deps across all subprojects |
| `make proto` | Regenerate Go + TS code from `.proto` files |
| `make up` | Start cockroach + backend + ai service |
| `make down` | Stop all services |
| `make logs` | Tail logs from all services |
| `make clean` | Remove generated code and containers |

## Build plan

See `ROADMAP.md` for the phased plan. High-level:

- **Phase 0:** Monorepo scaffolding. Everything boots; nothing does real work.
- **Phase 1:** Thin end-to-end slice with a hardcoded 2-task DAG and stub executor.
- **Phase 2:** Real AI planner generates the DAG.
- **Phase 3:** Real task execution with the worker pool (the meaty Go phase).
- **Phase 4:** Resilience — timeouts, retries, cancellation, graceful shutdown.
- **Phase 5:** Polish — nicer DAG viz, streaming token render, run history.

## Scope guardrails

To keep this finishable:

- Max ~20 tasks per DAG.
- Three task kinds only: `ai`, `fetch`, `transform`.
- Single-node backend. No multi-node coordination.
- Single AI service instance.
- Local Cockroach in Docker, single node.
- No auth. Single-user local tool.

## Explicit non-goals

- Horizontal scaling
- Multi-tenancy / auth / user accounts
- Arbitrary code execution in tasks (security nightmare, wrong scope)
- Rich task library beyond the three kinds
- Sophisticated planning (no ReAct loops, no replanning mid-run for v1)
- Cost / token accounting (stretch goal)
- Persistent task queues across backend restarts (stretch goal)
