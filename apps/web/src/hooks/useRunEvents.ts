import { useEffect, useReducer, useRef } from "react";
import {
  getRunDetail,
  runEventsURL,
  type Run,
  type RunDetail,
  type RunEvent,
  type RunStatus,
  type Task,
  type TaskStatus,
} from "#/lib/api";

// ConnectionState tells the UI whether the event stream is live. The
// browser's EventSource auto-reconnects, so "reconnecting" is transient.
export type ConnectionState =
  | "loading" // fetching initial snapshot
  | "open" // EventSource connected
  | "reconnecting" // EventSource readyState == CONNECTING after an error
  | "closed" // we or the server closed it
  | "error"; // fetch or parse failure

type State = {
  run: Run | null;
  tasks: Task[];
  events: RunEvent[]; // kept for a live log panel
  connection: ConnectionState;
  error: string | null;
};

type Action =
  | { type: "hydrate"; detail: RunDetail }
  | { type: "event"; event: RunEvent }
  | { type: "connection"; state: ConnectionState }
  | { type: "error"; message: string };

const initialState: State = {
  run: null,
  tasks: [],
  events: [],
  connection: "loading",
  error: null,
};

// applyEvent is the core state transition: given the current run and
// tasks, figure out what this event changes. Keeping this logic in one
// place (as opposed to scattered setState calls) is the whole reason to
// reach for useReducer here.
function applyEvent(state: State, event: RunEvent): State {
  const tasks = [...state.tasks];
  let run = state.run;

  // Find a task by id if the event is task-scoped.
  const taskIdx = event.task_id
    ? tasks.findIndex((t) => t.id === event.task_id)
    : -1;
  const patchTask = (status: TaskStatus, extra: Partial<Task> = {}) => {
    if (taskIdx < 0) return;
    tasks[taskIdx] = { ...tasks[taskIdx], status, ...extra };
  };
  const patchRun = (status: RunStatus, extra: Partial<Run> = {}) => {
    if (!run) return;
    run = { ...run, status, ...extra };
  };

  switch (event.kind) {
    case "run_started":
      patchRun("running");
      break;
    case "run_completed":
      patchRun("completed", { completed_at: event.created_at });
      break;
    case "run_failed":
      patchRun("failed", { completed_at: event.created_at });
      break;
    case "run_cancelled":
      patchRun("cancelled", { completed_at: event.created_at });
      break;
    case "task_started":
      patchTask("running", { started_at: event.created_at });
      break;
    case "task_completed":
      patchTask("completed", { completed_at: event.created_at });
      break;
    case "task_failed":
      patchTask("failed", {
        completed_at: event.created_at,
        error: String((event.payload as { error?: string }).error ?? ""),
      });
      break;
  }

  return {
    ...state,
    run,
    tasks,
    events: [...state.events, event],
  };
}

function reducer(state: State, action: Action): State {
  switch (action.type) {
    case "hydrate":
      return {
        ...state,
        run: action.detail.run,
        tasks: action.detail.tasks,
        connection: "open",
        error: null,
      };
    case "event":
      return applyEvent(state, action.event);
    case "connection":
      return { ...state, connection: action.state };
    case "error":
      return { ...state, connection: "error", error: action.message };
  }
}

// useRunEvents hydrates the run, subscribes to its SSE stream, and keeps
// the returned state in sync with both sources.
//
// Lifecycle:
//  1. Fetch GET /runs/:id for the initial snapshot (run + tasks).
//  2. Open EventSource for /runs/:id/events.
//  3. Each SSE message -> reducer dispatch.
//  4. On unmount or runId change, close the EventSource.
export function useRunEvents(runId: string | null) {
  const [state, dispatch] = useReducer(reducer, initialState);
  // Keep the EventSource in a ref so we can always close exactly one.
  const sourceRef = useRef<EventSource | null>(null);

  useEffect(() => {
    if (!runId) return;

    let cancelled = false;

    // --- Hydrate ---
    getRunDetail(runId)
      .then((detail) => {
        if (cancelled) return;
        dispatch({ type: "hydrate", detail });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        dispatch({ type: "error", message: String(err) });
      });

    // --- Subscribe ---
    // Open the EventSource immediately; any events that arrive before
    // hydration finishes are queued by the reducer and applied after.
    const source = new EventSource(runEventsURL(runId));
    sourceRef.current = source;

    source.onopen = () => dispatch({ type: "connection", state: "open" });
    source.onerror = () => {
      // readyState === 0 means CONNECTING (auto-reconnect in flight).
      // === 2 means CLOSED (giving up).
      const s = source.readyState === 0 ? "reconnecting" : "closed";
      dispatch({ type: "connection", state: s });
    };

    // addEventListener("kind", ...) matches the SSE `event: kind` lines
    // our backend emits. One listener per kind.
    const kinds: Array<RunEvent["kind"]> = [
      "run_started",
      "run_completed",
      "run_failed",
      "run_cancelled",
      "task_started",
      "task_completed",
      "task_failed",
    ];
    const handler = (e: MessageEvent) => {
      try {
        const event = JSON.parse(e.data) as RunEvent;
        dispatch({ type: "event", event });
      } catch (err) {
        dispatch({ type: "error", message: `bad event: ${String(err)}` });
      }
    };
    for (const k of kinds) source.addEventListener(k, handler);

    return () => {
      cancelled = true;
      for (const k of kinds) source.removeEventListener(k, handler);
      source.close();
      sourceRef.current = null;
    };
  }, [runId]);

  return state;
}
