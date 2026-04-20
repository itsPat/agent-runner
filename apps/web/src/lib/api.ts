// API types — these mirror the DTO shapes emitted by the Go httpapi
// adapter. If a backend field is added, add it here too.

export type RunStatus =
  | "pending"
  | "running"
  | "completed"
  | "failed"
  | "cancelled";

export type TaskKind = "ai" | "fetch" | "transform";

export type TaskStatus =
  | "pending"
  | "ready"
  | "running"
  | "completed"
  | "failed"
  | "cancelled";

export type Run = {
  id: string;
  goal: string;
  status: RunStatus;
  created_at: string;
  updated_at: string;
  completed_at?: string | null;
};

export type Task = {
  id: string;
  run_id: string;
  kind: TaskKind;
  spec: Record<string, unknown>;
  depends_on: string[];
  status: TaskStatus;
  result?: Record<string, unknown> | null;
  error?: string;
  attempts: number;
  created_at: string;
  started_at?: string | null;
  completed_at?: string | null;
};

export type RunDetail = {
  run: Run;
  tasks: Task[];
};

// Event kinds we care about. Kept as a discriminated union so reducers
// can exhaustively switch on kind.
export type EventKind =
  | "run_started"
  | "run_completed"
  | "run_failed"
  | "run_cancelled"
  | "task_started"
  | "task_completed"
  | "task_failed";

export type RunEvent = {
  id: string;
  seq: number;
  run_id: string;
  task_id?: string | null;
  kind: EventKind;
  payload: Record<string, unknown>;
  created_at: string;
};

const BACKEND_URL = (import.meta.env.VITE_BACKEND_URL as string) || "";

export async function submitGoal(goal: string): Promise<Run> {
  const res = await fetch(`${BACKEND_URL}/runs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ goal }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(body.error ?? "submit failed");
  }
  return (await res.json()) as Run;
}

export async function getRunDetail(id: string): Promise<RunDetail> {
  const res = await fetch(`${BACKEND_URL}/runs/${id}`);
  if (!res.ok) {
    throw new Error(`GET /runs/${id}: ${res.status}`);
  }
  return (await res.json()) as RunDetail;
}

export function runEventsURL(id: string): string {
  return `${BACKEND_URL}/runs/${id}/events`;
}
