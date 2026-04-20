import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { submitGoal } from "#/lib/api";
import { Button } from "#/components/ui/button";
import { Textarea } from "#/components/ui/textarea";
import { Label } from "#/components/ui/label";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "#/components/ui/card";

export const Route = createFileRoute("/")({
  component: Home,
});

function Home() {
  const navigate = useNavigate();
  const [goal, setGoal] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!goal.trim()) return;
    setSubmitting(true);
    setError(null);
    try {
      const run = await submitGoal(goal.trim());
      await navigate({ to: "/runs/$runId", params: { runId: run.id } });
    } catch (err) {
      setError(String(err));
      setSubmitting(false);
    }
  }

  return (
    <div className="p-8">
      <div className="mx-auto max-w-xl">
        <Card>
          <CardHeader>
            <CardTitle>agent-runner</CardTitle>
            <CardDescription>
              Submit a goal. The backend will plan it as a DAG of tasks
              and stream live progress.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <form onSubmit={onSubmit} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="goal">Goal</Label>
                <Textarea
                  id="goal"
                  placeholder="Research the top 3 EV battery companies and summarize their 2025 financials."
                  value={goal}
                  onChange={(e) => setGoal(e.target.value)}
                  disabled={submitting}
                  rows={4}
                  autoFocus
                />
              </div>
              {error && (
                <p className="text-sm text-destructive">{error}</p>
              )}
              <div className="flex justify-end">
                <Button type="submit" disabled={submitting || !goal.trim()}>
                  {submitting ? "submitting..." : "Run"}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
