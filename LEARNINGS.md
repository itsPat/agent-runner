# Learnings: Distributed AI Agent Runner

> **Purpose:** A running synthesis of what we've learned while building this project, organized by phase. A companion to `PROJECT_SPEC.md` (what we're building) and `ROADMAP.md` (the phased plan).
>
> **Last updated:** end of Phase 2.

---

## How to read this document

Each phase section distills:

- **What we built** — a one-paragraph summary of the slice.
- **Teachable bits** — numbered, concrete lessons. Prefer specifics (file paths, function names, exact patterns) over vague principles.
- **Decisions that weren't obvious** — moments where the "textbook" answer was wrong and we picked the better one.
- **Deliberate omissions** — things we consciously didn't build yet, and why. Often more important than what we did build.
- **Three takeaways** — the reductions you should remember if you remember nothing else.

The cross-cutting themes section at the end pulls out patterns that recur across multiple phases.

---

## Phase 0 — monorepo + infra scaffolding

**What we built.** Repo layout, Docker Compose with Cockroach + runner + AI service, `buf` for protobuf codegen, ConnectRPC for the Runner ↔ AI service boundary, TanStack Start + shadcn for the web app, and a health-check flow proving the wiring end-to-end.

**Teachable bits.**

1. **`buf` over raw `protoc`.** `buf` handles plugin version pinning, linting, and breaking-change detection. One `buf.gen.yaml` replaces a page of `protoc` flags.
2. **ConnectRPC is protobuf over plain HTTP.** Runs over `net/http` with no separate gRPC server. Simpler than gRPC, same type safety. The generated clients look native to each language.
3. **Committed `gen/` directory.** Generated code is checked in. Controversial in some communities, but for a small monorepo it means `git clone && go build` works without a codegen step.
4. **Runner runs in Docker; frontend runs locally.** Docker Compose boots Cockroach + runner + AI service. Web app runs via `bun dev` on the host for fast HMR. The right tradeoff per piece.
5. **Single-node Cockroach with in-memory store.** `--store=type=mem` makes dev instant — no data persists across restarts, which is what you want for fast iteration.

**Three takeaways.**

- Monorepo scaffolding *always* takes longer than expected; budget a full weekend for Phase 0.
- Protobuf+ConnectRPC pays off the moment the wire format matters — picking it up front saves a migration later.
- "Run the thing you're testing the way it'll run in production" is worth the Docker overhead; "run the thing you're iterating on as natively as possible" is worth the inconsistency.

---

## Phase 1 — thin end-to-end vertical slice

Phase 1 built the full user-facing loop end-to-end with a stubbed planner and stubbed task execution. Seven slices across six milestones.

### 1.1 — Cockroach schema + embedded migrations

**What we built.** The three core tables (`runs`, `tasks`, `events`) with CRDB-aware schema choices, plus `goose` as an embedded migration library so the runner binary self-migrates on startup.

**Teachable bits.**

1. **Migrations are an adapter concern.** They live with the Cockroach code (`adapters/cockroach/migrations/`), not at the project root. If you swapped CRDB for SQLite, this directory would go with it.
2. **`//go:embed` + `fs.Sub`.** `//go:embed migrations/*.sql` ships the SQL inside the binary. The `migrations/` path prefix is preserved in the embedded FS, so `fs.Sub(fs, "migrations")` is needed to scope to the subdirectory that goose expects.
3. **`TIMESTAMPTZ` always, `TIMESTAMP` never.** The latter silently drops timezone info and causes "why is this time wrong" bugs months later.
4. **`TEXT` + `CHECK` constraints over enums.** Postgres/CRDB enums exist but are awkward to evolve. `status TEXT CHECK (status IN (...))` gives the same safety with trivial migrations.
5. **`unique_rowid()` on CRDB, not `SERIAL`.** CRDB's distributed-safe monotonic INT8. On CRDB, `SERIAL` expands to a random UUID — same API as Postgres, different semantics. Classic footgun for Postgres veterans.
6. **Cargo-cult watch: `pg_advisory_lock` doesn't work on CRDB.** Goose's session locker uses it. We would have silently broken on a multi-runner setup. Honest fix: don't add machinery you can't actually use; document why.

**Decisions that weren't obvious.**

- **App-generated UUIDs, not DB defaults.** The schema has `DEFAULT gen_random_uuid()` as a safety net, but we pass IDs in from Go. Reason: Task A's `depends_on` must reference Task B's ID *before* persisting. Also: deterministic tests.
- **`depends_on UUID[]` on the task row, not a link table.** For DAGs ≤20 tasks, the array is simpler (one row per task, read the whole task). Link tables scale better — documented the escape hatch for later.
- **Foreign keys with `ON DELETE CASCADE`.** Deleting a run drops its tasks and events. Small write-path cost, big data integrity win.

**Deliberately omitted.**

- Advisory locks on migrations (single runner in Phase 1, and CRDB doesn't implement them anyway).
- Crashed-run recovery (stretch goal).

**Three takeaways.**

- Schema decisions aren't "just SQL" — they encode what the adapter can and can't do cleanly.
- Pick migration tooling based on deployment shape: embedded library for "single binary" apps, external CLI for "migrate in CI/CD."
- Document cargo-cult dodges — they're the places future-you will wonder why you left them out.

### 1.2 — domain types + TaskStore port + pgx repository

**What we built.** The hexagonal spine. `domain.Run`, `domain.Task`, `domain.DAG` as pure types. `ports.TaskStore` as a consumer-owned interface with three methods. `adapters/cockroach.TaskStore` as the pgx-backed implementation with atomic `CreateRun` via `pgx.BeginFunc`.

**Teachable bits.**

1. **Domain is storage-agnostic.** No `db` tags, no `json` tags on domain types. Those live on *adapter-local* DTO structs (`runRow`, `taskRow`).
2. **Interfaces belong to consumers.** `ports.TaskStore` is defined next to the code that calls it, not next to the adapter that implements it. Go's structural typing makes this the right shape. In Java/C# you'd do the opposite.
3. **Compile-time interface assertions.** `var _ ports.TaskStore = (*TaskStore)(nil)` turns "I forgot a method" from a runtime error into a build error. Single line, high payoff.
4. **`pgx.BeginFunc` for clean transactions.** Commits on nil return, rolls back on error, rolls back on panic. Pattern of choice for any pgx transaction.
5. **Translate driver errors at the port boundary.** `pgx.ErrNoRows` → `ports.ErrNotFound`. The app layer does `errors.Is(err, ports.ErrNotFound)` without ever importing pgx.
6. **`pgx.CollectRows` + `pgx.RowToStructByName`.** The modern pgx way to scan into structs with `db:"col_name"` tags. Clean and typed.
7. **pgx native codecs for `UUID[]` and `JSONB`.** No conversion boilerplate — declare `[]uuid.UUID` and `json.RawMessage` on the DTO and it Just Works.

**Decisions that weren't obvious.**

- **`DAG` as a named type.** Wrapping `Run + []Task` in a `DAG` that validates cycles on construction means downstream code never handles a half-valid graph. The invariant is in the type system.
- **3-color DFS for cycle detection.** Classic algorithm, read as: white = unvisited, gray = on current path, black = fully explored. Hitting gray means a back-edge.
- **Atomic `CreateRun(dag DAG) error`.** One method, one transaction, one consistency boundary. Separate `CreateRun` + `CreateTask` would have leaked "remember both" to the app.

**Deliberately omitted.**

- `UpdateTaskStatus`, `ListReadyTasks` — nothing needed them yet. Added in 1.4 when the executor actually does.
- EventStore — deferred until 1.5 when cursor resume demanded persistence.

**Three takeaways.**

- Grow interfaces by use, not speculation. Three methods today; more when a caller demands them.
- Named domain types with validated constructors catch bugs the compiler can't.
- Keep `domain` pure: no SQL tags, no JSON tags, no infra imports. The adapter owns translation.

### 1.3a — HTTP handlers (POST /runs, GET /runs/:id)

**What we built.** The first user-facing endpoints. `app.RunService` with `SubmitGoal` and `GetRunDetail`. The `httpapi` adapter with its own JSON DTOs, `Router.Register(mux)` pattern, and error translation.

**Teachable bits.**

1. **Adapter-owned DTOs.** The HTTP layer defines its own structs (`runDTO`, `taskDTO`) with `json` tags. Even when they look identical to the domain types today — they diverge the moment HTTP wants `omitempty` or snake_case.
2. **Error translation at the boundary.** `ports.ErrNotFound` → HTTP 404. Validation errors → 400. Unknown → 500. The handler is the *only* place HTTP status codes appear.
3. **Go 1.22+ method-aware mux.** `mux.HandleFunc("POST /runs", h)` and `{id}` path params with `r.PathValue("id")`. Stdlib is enough — no router dep.
4. **`adapter.Register(mux)` pattern.** The HTTP adapter owns its path patterns; `main.go` doesn't repeat them. Clean and idiomatic.
5. **Handler as method on a struct.** Same reason as use cases: deps wired at construction, handlers are methods on the receiver. Tests use fakes by swapping the struct.

**Decisions that weren't obvious.**

- **One `RunService` over many per-use-case structs.** Textbook Clean Architecture says "one interactor per use case." Idiomatic Go says "one service holding related methods when they share deps." Go wins when deps are shared, which is almost always.

**Deliberately omitted.**

- Authentication. Single-user local tool.
- Rate limiting. Same.
- OpenAPI / spec generation. Manual for now.

**Three takeaways.**

- Don't apply Java patterns unmodified to Go. Per-use-case structs, generic repository interfaces, and factory classes are overkill; pragmatic method-on-service is idiomatic.
- Adapter-owned DTOs decouple the transport from the domain cleanly. The translation cost is trivial; the decoupling pays off the first time they diverge.
- Error-to-status mapping is the handler's job, not the app's.

### 1.3b — EventBus port + in-memory pub/sub + SSE + stub emitter

**What we built.** The event pipeline. `domain.Event` type. `ports.EventBus` with context-as-lifetime `Subscribe`. `adapters/memeventbus` as a per-run fan-out. `adapters/httpapi/sse.go` for `GET /runs/:id/events`. A `StubEmitter` that fires `task_started`/`completed` events so we could test end-to-end before the real executor existed.

**Teachable bits — the densest slice so far.**

1. **Context as subscription lifetime.** `Subscribe(ctx, runID) <-chan Event` — when the caller's `ctx` is cancelled (client disconnect, server shutdown), the bus removes the subscriber and closes the channel. No explicit `Unsubscribe()` to forget. This is the most Go-idiomatic pattern in the project.
2. **Receive-only channel in the return type.** `<-chan Event`, not `chan Event`. Catches `close(ch)` and accidental `ch <- x` at compile time.
3. **Non-blocking send with `select + default`.** A slow subscriber can't stall the publisher. Drop events for the lagger, keep everyone else healthy.
4. **Remove-then-close inside a write lock.** The publisher holds the RLock; the cleanup holds the write lock. Removing from the map first, then closing, ensures no publisher is mid-send when the channel closes. Without this you get "send on closed channel" panics.
5. **SSE minimums.** `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `X-Accel-Buffering: no` (the last one defeats nginx's buffering). `http.Flusher.Flush()` after every write or the browser sees nothing live.
6. **Heartbeats every 25s.** `: keepalive\n\n` (a comment, ignored by the browser). Survives proxies and laptops going to sleep. Five lines of code, production-quality habit.
7. **`<-r.Context().Done()` as the disconnect signal.** Go's HTTP server cancels the request context when the client goes away. `select` between "new event" and "ctx done" — that's how the SSE goroutine cleans up.
8. **Buffered channel per subscriber.** 64 events deep. Absorbs bursts, bounds memory, drops to catch up under pressure.

**Decisions that weren't obvious.**

- **`Executor` interface lives in `app/`, not `ports/`.** Ports are for *external* concerns (DBs, buses, transports). The executor is app-internal orchestration — it lives *with* the app layer, not at its boundary.

**Deliberately omitted.**

- Event persistence. Deferred to 1.5 along with cursor resume.
- Real task execution. Deferred to 1.4 — the stub emitter proves the pub/sub path in isolation.

**Three takeaways.**

- Use context as the lifetime of a thing (subscription, goroutine, transaction) wherever you can. Cleanup becomes automatic.
- Mutex discipline in pub/sub: publishers hold RLock; the thing that can close channels holds write-lock. Remove-then-close, always.
- SSE works great in stdlib — the gotchas are all in headers and flushing, not in the protocol itself.

### 1.4 — real DAG executor with DB state transitions

**What we built.** Replaced the stub emitter with a real executor. Added `DAG.TopologicalOrder()` via Kahn's algorithm. Extended `TaskStore` with six specific-transition methods (`MarkRunRunning`, `MarkRunCompleted`, `MarkRunFailed`, `MarkTaskRunning`, `MarkTaskCompleted`, `MarkTaskFailed`). The executor walks tasks in topological order, updates DB state, publishes events.

**Teachable bits.**

1. **Kahn's algorithm for execution order.** DFS handled cycle *detection* in `NewDAG`; Kahn's handles *ordering* here. Queue-based wave-by-wave peel reads naturally as "what's ready now?"
2. **Specific methods per state transition beat a generic `UpdateStatus`.** `MarkTaskRunning(id, at)` and `MarkTaskCompleted(id, result, at)` document valid transitions and force callers to supply the exact fields each state needs.
3. **DB-first, then publish.** If publish fails, events are missing but the DB is correct — the lesser evil. The reverse (publish before DB) risks broadcasting state that doesn't exist.
4. **Postcondition panics on invariant violations.** `TopologicalOrder` panics if Kahn's doesn't visit every task — because `NewDAG` already guaranteed no cycles, so hitting that branch means a bug, not an expected failure. Go idiom: panics for bugs, errors for expected failures.
5. **Two sources of truth kept consistent.** SSE clients and `GET /runs/:id` tell the same story because the DB update always precedes the Publish. This discipline matters for any event-sourced or pub/sub system.

**Decisions that weren't obvious.**

- **Dropped the `Executor` interface once the stub was gone.** The interface existed to swap stub→real. Once the stub was deleted, the interface abstracted nothing. `RunService` now takes `*Executor` concrete. Add an interface when testing or swapping actually demands it — not before.

**Deliberately omitted.**

- Worker pool pattern. Still single-threaded sequential per run. Phase 3 adds N workers + `SELECT ... FOR UPDATE SKIP LOCKED`.
- Real task execution. Still `time.Sleep` as the "work." Phase 3 replaces this with `fetch` / `transform` / `ai` implementations.
- Per-run cancellation context. Executor uses `context.Background()` with a 2m budget. Phase 4 wires cancellation through.
- Retries on failure. Phase 4.

**Three takeaways.**

- DFS and Kahn's are complementary: one for detection, one for ordering. Both short, both teachable.
- "Specific methods per transition" beats "generic UpdateStatus with optional fields." The method name *is* the documentation.
- When an interface stops abstracting anything real, drop it. Interfaces earn their place by being implemented more than once.

### 1.5 — persistent events + SSE cursor resume

**What we built.** Added `domain.Event.Seq int64`. Created `ports.EventStore` with `Append` (persist and back-fill seq via `INSERT ... RETURNING`) and `ListSince`. The executor's new `emit` helper does persist-then-publish. The SSE handler now does **subscribe-first, replay-history, live-with-dedup** using the SSE-native `Last-Event-ID` header.

**Teachable bits — this slice had the deepest distributed-systems content.**

1. **Subscribe-first, replay-history, live-with-dedup.** The canonical pattern for durable event streams. Reversing the order (replay first, then subscribe) leaves a gap where events published during the replay are lost. Subscribing first means they land in the buffered channel and get deduped on the way out.
2. **Persist-then-publish ordering discipline.** Persist failure → skip publish, but DB still authoritative (cursor-resume clients will eventually see it). Publish failure → DB still correct. Never publish-before-persist.
3. **Cursor opacity.** Clients don't invent cursor values — they echo back whatever `id:` the server sent. This is why `unique_rowid()`'s gigantic non-sequential IDs don't matter: the client never has to interpret them. (I tripped on this assumption in the first verification — good reminder.)
4. **`unique_rowid()` vs `SERIAL`, reminded.** CRDB's unique_rowid() produces big monotonic INT8s that encode timestamp + node_id. Not 1,2,3. Production deployments assume this.
5. **`INSERT ... RETURNING` as a single-round-trip idiom.** The DB picks defaults (`seq`, `created_at`); `RETURNING` reports them back so the Go struct stays in sync. Beats two round-trips or reading-your-own-write.
6. **`id: <seq>` in each SSE message.** Tells the browser's `EventSource` to track the cursor. Browsers auto-send it as `Last-Event-ID` on reconnect. Basically free resume-on-reconnect.
7. **`Last-Event-ID` is the SSE-native cursor header.** Browsers set it on reconnection automatically. Accepting it via request header is idiomatic; query params would be a bolt-on.

**Decisions that weren't obvious.**

- **`Seq int64` promoted to the domain type.** Earlier I argued seq was storage-only and didn't belong on `domain.Event`. Now a reader (SSE handler) genuinely needs it for cursor semantics, so it graduates. Test: does *behavior* depend on it? When it does, promote. When it doesn't, keep it in adapter-land.
- **Two ports, not one.** `EventBus` (pub/sub) and `EventStore` (persistence+query) serve different readers with different cost models. Merging them blurs what's cheap vs. expensive and invites mistakes.

**Deliberately omitted.**

- Durable pub/sub (Redis, NATS). Single-node runner means in-memory is enough.
- Server-side deduplication of duplicate publishes. The SSE handler's client-side dedup is sufficient for Phase 1.

**Three takeaways.**

- The subscribe-replay-live ordering is worth internalizing — you'll reach for it in every event-sourced system.
- When something becomes reader-visible, promote it to the domain. Storage-purity is a good default, not a hard rule.
- SSE's `id:` / `Last-Event-ID` gives you resume-on-reconnect for a few lines of code. Design streams to support it from the start.

### 1.6 — web app: form + live run detail page

**What we built.** Home page rewritten from a health check into a goal submission form. New route `/runs/$runId` with a live-updating detail view. Custom `useRunEvents` hook (hydrate-then-listen with `useReducer`). Six shadcn primitives installed.

**Teachable bits.**

1. **Hydrate-then-listen.** `GET /runs/:id` provides fields events don't carry (task `kind`, `spec`, `depends_on`). EventSource provides live status transitions. Two data sources, one reducer-managed state.
2. **`useReducer` for event-driven state.** Events map one-to-one onto actions. Centralizes "what does this event change" in one place — scattering `setState` calls across handlers gets ugly fast.
3. **`EventSource` + `useEffect` cleanup.** The browser auto-reconnects for free. Our job is to call `source.close()` on unmount (and on `runId` change). Missing this leaks connections on every navigation.
4. **File-based routing with `$param`.** `routes/runs.$runId.tsx` → `/runs/:runId`, read with `Route.useParams()`. Typed, no router lib beyond TanStack's plugin.
5. **shadcn is components-you-own.** Primitives are copied into `src/components/ui/`. No runtime lib to upgrade-break. Edit freely.
6. **`EventSource.readyState` tells you the connection story.** `0=CONNECTING`, `1=OPEN`, `2=CLOSED`. Expose this as a user-visible pill so "reconnecting..." looks different from "disconnected."

**Decisions that weren't obvious.**

- **Typed API client in `lib/api.ts` mirrors the backend DTOs exactly.** `run_id`, `created_at`, snake_case — the frontend types match what comes over the wire rather than being idealized. Single source of truth for wire shapes.
- **StrictMode's double-render caused `GET /runs/:id` twice in dev.** Not a bug — React 19's way of surfacing effect-cleanup correctness. Harmless because GET is idempotent.

**Deliberately omitted.**

- Graph rendering (react-flow, etc.). A list is fine for Phase 1; visual DAG comes in Phase 5.
- Optimistic UI on form submit. Good enough without.
- Run history page. Phase 5.

**Three takeaways.**

- Hydrate-then-listen is the right pattern whenever your stream doesn't carry *every* field the UI needs.
- Reach for `useReducer` when state transitions feel action-shaped. Scatters of `setState` inside event handlers are a smell.
- EventSource + `Last-Event-ID` + useEffect cleanup is maybe 50 lines total, and it gives you live-updating UI that survives network blips. Huge bang-for-buck.

---

## Phase 2 — real AI planner

**What we built.** Replaced `stubDAGForGoal` with a live LLM-generated DAG. Added `PlanGoal` RPC to the proto. Implemented `planGoal` in the TS AI service using ai-sdk 6's `generateText` + `Output.object`. Created `ports.AIPlanner` and `adapters/grpcai.Planner`. Iterated the Zod schema to a discriminated union when the first attempt returned empty specs. Swapped Anthropic for OpenRouter (via the OpenAI-compatible ai-sdk provider pointed at `https://openrouter.ai/api/v1`).

**Teachable bits.**

1. **ai-sdk 6 changed the structured-output API.** `generateObject` is gone; use `generateText({ output: Output.object({ schema }) })` instead. Breaking change worth knowing if you learned ai-sdk on the old API.
2. **OpenRouter is OpenAI-API-compatible.** `createOpenAI({ baseURL: "https://openrouter.ai/api/v1", apiKey, name: "openrouter" })` is the whole integration. No dedicated provider needed. Model IDs follow `provider/model` convention (e.g. `anthropic/claude-sonnet-4.5`). More transferable learning than a bespoke OpenRouter plugin.
3. **Zod `.refine()` doesn't round-trip through JSON Schema.** Structured-output endpoints consume a subset of JSON Schema that covers primitives, enums, arrays, objects, unions — but not custom predicates. A `.refine()` makes the outer Zod reject what the endpoint's schema accepted, causing hard failures rather than retries. Fix: model the constraint *structurally* (discriminated unions, enums, nested objects), not with custom predicates.
4. **Zod `.default({})` reads as "field is optional" to the model.** When the schema said `spec: z.record(z.string(), z.any()).default({})`, the model happily returned `{}` every time. JSON Schema `default` is a hint the model treats as "skip this."
5. **Discriminated unions fix "model skips details" problems.** Moving from `spec: record(string, any)` to `z.discriminatedUnion("kind", [FetchTaskSchema, TransformTaskSchema, AITaskSchema])` — each branch with its own required spec shape — made the model fill in `url` for fetch, `instruction` for ai, `op` for transform. The JSON Schema `oneOf` forces the model to pick a branch and satisfy its required fields.
6. **Two-pass name → UUID mapping in the adapter.** The LLM references tasks by string name (`"fetch_catl_wiki"`); our domain uses UUIDs. Pass 1: assign UUIDs to every name up front. Pass 2: translate each task, resolving `depends_on` names through the map. Without pass 1, forward references (depending on a not-yet-seen task) would fail.
7. **Return the validated `domain.DAG`, not `[]Task`.** The adapter calls `NewDAG` as its final gate so callers can trust what they receive. Keeps cycle/reference validation in one place (the domain) rather than duplicated across every adapter.
8. **Defensive wall count: two.** TS-side Zod validates structure; Go-side `NewDAG` validates DAG invariants. Two walls because the LLM is an untrusted input and each language can check different things cheaply.
9. **Prompt + schema as parallel sources of truth.** The system prompt *says* `spec` must be non-empty and specific. The schema *forces* it via required fields. Either alone is weaker — prompts can be ignored, schemas without rich structure are too permissive. Both together are robust.
10. **Makefile loading `.env` for local dev.** Docker Compose auto-loads `.env` from its cwd. For `make ai-run` to match behavior, a tiny `ENV_LOADER = set -a; [ -f .env ] && . ./.env; set +a;` prefix makes targets pick up the same file. Repo root `.env`, git-ignored.

**Decisions that weren't obvious.**

- **Planning runs synchronously inside `SubmitGoal`, not on a background goroutine.** Planning takes a few seconds — slower than typical HTTP but fast enough to sit on the hot path, and makes error surfacing trivial (4xx on planner failure). Alternative (plan async, update run when ready) would require an intermediate "planning" run state plus a separate event on plan completion. Not worth the complexity for sub-10s planning.
- **Specs as JSON string in the proto, not `google.protobuf.Struct`.** `Struct` carries awkward TS↔Go type gymnastics. String + `JSON.stringify`/`json.RawMessage` has zero ceremony on either side. Semantic loss is nil — the payload is opaque anyway.
- **Dropped the `@ai-sdk/anthropic` package entirely.** When we swapped to OpenRouter, the Anthropic provider would have been dead code. Remove, don't keep "just in case."

**Deliberately omitted.**

- `ExecuteTask` RPC. Phase 3 — that's where task kinds actually run rather than sleep.
- `Summarize` RPC. Phase 3, for the final answer assembly.
- Streaming responses. Phase 3, when AI tasks emit tokens live.
- Planner retries on model errors. ai-sdk retries schema mismatches automatically; we don't retry network errors explicitly. Add if flakiness hits.
- Planner caching. Same goal submitted twice → two LLM calls. Could cache by goal hash but Phase 1's runs are rarely identical.

**Three takeaways.**

- **Model the structure you want the LLM to produce.** Discriminated unions over `record(string, any)`. Required fields over defaulted ones. The schema is a teacher, not just a validator.
- **JSON Schema is a lowest-common-denominator.** Keep Zod schemas to what round-trips cleanly. Validate richer invariants separately, either in the prompt or after parsing.
- **OpenRouter is just OpenAI-compatible HTTP.** Learning the "generic OpenAI provider pointed at base URL" pattern transfers to Groq, Together, Fireworks, self-hosted llama.cpp, and any future gateway. A dedicated provider per gateway is rarely worth the extra package.

---

## Cross-cutting themes

Patterns that recurred across multiple phases and probably will again.

### Hexagonal, pragmatically

- Dependency arrow always points inward: `cmd → adapters → app → ports → domain`. Domain imports stdlib + `google/uuid` and nothing else.
- **Ports belong to consumers.** Define the interface where it's called, not where it's implemented.
- **Not everything is a port.** Reserve `ports/` for external concerns (DB, bus, transport). Internal orchestration (the executor) stays in `app/` as concrete code or app-local interfaces.
- **Composition happens in one place.** `cmd/server/main.go` is the only file that imports every adapter concretely.
- **Translate at the boundary.** Driver errors → port errors. Domain types → HTTP DTOs. SQL rows → domain types. Tedious; entirely the point.
- **Grow by use, not speculation.** Three methods today, six tomorrow when a caller demands them.

### Go idioms earned their keep

- **Context as lifetime.** Goroutines, subscriptions, HTTP requests, transactions — they all live as long as their context. Clean cleanup for free.
- **Channel direction in return types.** `<-chan T` prevents caller misuse at compile time.
- **`select + default` for non-blocking sends.** Producer never stalls on a slow consumer.
- **Mutex discipline in pub/sub.** RLock for publishers, write-lock for cleanup. Remove-then-close.
- **Typed string aliases + constants.** `type RunStatus string` plus `RunStatusPending`, etc. Autocomplete + method attachment point.
- **Compile-time interface assertions.** `var _ Iface = (*T)(nil)`. Cheap insurance against drift.
- **Go 1.22+ method-aware mux.** Stdlib is enough until middleware chains demand more.
- **Goroutine-per-something.** Per subscriber, per run, per HTTP request. Always watch `ctx.Done()`.
- **Drop interfaces that abstract nothing.** Interfaces earn their place by being implemented more than once (or faked for tests).

### Database craft (CRDB + pgx)

- `TIMESTAMPTZ` always; never `TIMESTAMP`.
- `TEXT` + `CHECK` over enums.
- `unique_rowid()` over `SERIAL` on CRDB.
- `ON DELETE CASCADE` on child FKs for data integrity.
- pgx native API over `database/sql` — transactions via `BeginFunc`, scanning via `CollectRows` + `RowToStructByName`, native codecs for `UUID[]` and `JSONB`.
- Translate `pgx.ErrNoRows` → `ports.ErrNotFound` at the adapter boundary.
- Watch out for CRDB quirks: `pg_advisory_lock` doesn't work; `SERIAL` doesn't mean what you think.
- `INSERT ... RETURNING` is the idiomatic single-round-trip for defaulted columns.

### Event-driven correctness

- **Subscribe-first, replay-history, live-with-dedup.** The pattern for durable streams.
- **Persist-then-publish.** DB is authority; broadcast is best-effort.
- **Cursors are opaque to clients.** Echo, don't invent.
- **Two sources of truth must stay consistent** via a single discipline (persist first, then notify).

### Frontend patterns

- **Hydrate-then-listen.** Fetch gives you the full shape; stream keeps status fresh.
- **`useReducer` for event-shaped state.** Scattered `setState` calls are a smell.
- **`EventSource` + `useEffect` cleanup + `Last-Event-ID`** is cheap and powerful.
- **Typed wire-shape DTOs** mirror the backend exactly; frontend speaks the same language.

### Things we deliberately defer

Often more important than what we built. The common thread: we add machinery only when a caller actually needs it.

- No tests yet. Exploration first; tests when the shape stabilizes (Phase 3+).
- No auth. Single-user local tool.
- No observability stack (Prometheus, etc.). `slog` suffices.
- No worker pool yet. Sequential execution until Phase 3.
- No cancellation propagation yet. Phase 4.
- No retries yet. Phase 4.
- No real task execution — tasks sleep. Phase 3.

---

## How this doc gets updated

At the end of every phase (not every slice, every **phase**), append a new section with:

1. The phase name and one-paragraph "what we built."
2. 5–10 numbered teachable bits, grounded in specific code or decisions.
3. Decisions that weren't obvious (the "we almost did X, but..." moments).
4. Deliberate omissions and why.
5. Three takeaways — the reductions worth remembering.

Update the cross-cutting themes section only when a genuinely new pattern emerges; don't just restate existing ones.

Bump the "Last updated" line at the top.
