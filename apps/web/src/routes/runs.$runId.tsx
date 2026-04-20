import { createFileRoute, Link } from "@tanstack/react-router";
import { ArrowLeft, Circle, CheckCircle2, XCircle, Loader2 } from "lucide-react";
import { useRunEvents, type ConnectionState } from "#/hooks/useRunEvents";
import { Badge } from "#/components/ui/badge";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "#/components/ui/card";
import { Button } from "#/components/ui/button";
import type { RunStatus, Task, TaskStatus } from "#/lib/api";

export const Route = createFileRoute("/runs/$runId")({
  component: RunDetail,
});

function RunDetail() {
  const { runId } = Route.useParams();
  const { run, tasks, events, connection, error } = useRunEvents(runId);

  return (
    <div className="p-8">
      <div className="mx-auto max-w-3xl space-y-6">
        <div className="flex items-center justify-between">
          <Button asChild variant="ghost" size="sm">
            <Link to="/">
              <ArrowLeft className="mr-2 h-4 w-4" />
              New run
            </Link>
          </Button>
          <ConnectionPill state={connection} />
        </div>

        {error && (
          <Card className="border-destructive">
            <CardContent className="pt-6 text-sm text-destructive">
              {error}
            </CardContent>
          </Card>
        )}

        {run ? (
          <>
            <Card>
              <CardHeader>
                <div className="flex items-center justify-between gap-2">
                  <CardTitle className="text-base leading-snug">
                    {run.goal}
                  </CardTitle>
                  <RunStatusBadge status={run.status} />
                </div>
                <p className="text-xs text-muted-foreground">
                  run {run.id}
                </p>
              </CardHeader>
              <CardContent className="text-xs text-muted-foreground">
                <div>created {formatTime(run.created_at)}</div>
                {run.completed_at && (
                  <div>completed {formatTime(run.completed_at)}</div>
                )}
              </CardContent>
            </Card>

            <section className="space-y-2">
              <h2 className="text-sm font-semibold">Tasks</h2>
              <div className="space-y-2">
                {tasks.map((t, i) => (
                  <TaskRow key={t.id} task={t} index={i} tasks={tasks} />
                ))}
              </div>
            </section>

            <section className="space-y-2">
              <h2 className="text-sm font-semibold">
                Event log ({events.length})
              </h2>
              <div className="rounded-lg border bg-muted/30 p-3 max-h-64 overflow-auto text-xs font-mono space-y-1">
                {events.length === 0 ? (
                  <div className="text-muted-foreground">
                    waiting for events...
                  </div>
                ) : (
                  events.map((e) => (
                    <div key={e.id} className="flex gap-2">
                      <span className="text-muted-foreground">
                        seq {e.seq}
                      </span>
                      <span className="font-semibold">{e.kind}</span>
                      {e.task_id && (
                        <span className="text-muted-foreground">
                          {e.task_id.slice(0, 8)}
                        </span>
                      )}
                    </div>
                  ))
                )}
              </div>
            </section>
          </>
        ) : (
          !error && (
            <Card>
              <CardContent className="pt-6 text-sm text-muted-foreground">
                loading run...
              </CardContent>
            </Card>
          )
        )}
      </div>
    </div>
  );
}

function TaskRow({
  task,
  index,
  tasks,
}: {
  task: Task;
  index: number;
  tasks: Task[];
}) {
  const depLabel =
    task.depends_on.length === 0
      ? "root"
      : task.depends_on
          .map((id) => {
            const ix = tasks.findIndex((t) => t.id === id);
            return ix >= 0 ? `task ${ix + 1}` : id.slice(0, 8);
          })
          .join(", ");

  return (
    <Card>
      <CardContent className="py-3">
        <div className="flex items-center gap-3">
          <TaskStatusIcon status={task.status} />
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium">
                task {index + 1}
              </span>
              <Badge variant="outline" className="text-xs">
                {task.kind}
              </Badge>
            </div>
            <div className="text-xs text-muted-foreground mt-0.5">
              deps: {depLabel}
              {task.attempts > 0 && (
                <span className="ml-2">attempts: {task.attempts}</span>
              )}
            </div>
            {task.error && (
              <div className="text-xs text-destructive mt-1 truncate">
                {task.error}
              </div>
            )}
          </div>
          <TaskStatusBadge status={task.status} />
        </div>
      </CardContent>
    </Card>
  );
}

function TaskStatusIcon({ status }: { status: TaskStatus }) {
  switch (status) {
    case "running":
      return <Loader2 className="h-4 w-4 animate-spin text-blue-500" />;
    case "completed":
      return <CheckCircle2 className="h-4 w-4 text-emerald-500" />;
    case "failed":
    case "cancelled":
      return <XCircle className="h-4 w-4 text-destructive" />;
    default:
      return <Circle className="h-4 w-4 text-muted-foreground" />;
  }
}

function RunStatusBadge({ status }: { status: RunStatus }) {
  const variant: "default" | "secondary" | "destructive" | "outline" =
    status === "completed"
      ? "default"
      : status === "failed" || status === "cancelled"
      ? "destructive"
      : status === "running"
      ? "secondary"
      : "outline";
  return <Badge variant={variant}>{status}</Badge>;
}

function TaskStatusBadge({ status }: { status: TaskStatus }) {
  const variant: "default" | "secondary" | "destructive" | "outline" =
    status === "completed"
      ? "default"
      : status === "failed" || status === "cancelled"
      ? "destructive"
      : status === "running"
      ? "secondary"
      : "outline";
  return (
    <Badge variant={variant} className="text-xs">
      {status}
    </Badge>
  );
}

function ConnectionPill({ state }: { state: ConnectionState }) {
  const label =
    state === "open"
      ? "live"
      : state === "reconnecting"
      ? "reconnecting…"
      : state === "loading"
      ? "connecting…"
      : state === "closed"
      ? "disconnected"
      : "error";
  const color =
    state === "open"
      ? "bg-emerald-500"
      : state === "reconnecting" || state === "loading"
      ? "bg-amber-500"
      : "bg-muted-foreground";
  return (
    <div className="flex items-center gap-2 text-xs text-muted-foreground">
      <span className={`inline-block h-2 w-2 rounded-full ${color}`} />
      {label}
    </div>
  );
}

function formatTime(iso: string): string {
  try {
    return new Date(iso).toLocaleTimeString();
  } catch {
    return iso;
  }
}
