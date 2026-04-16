import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useState } from "react";

export const Route = createFileRoute("/")({
  component: Home,
});

type HealthStatus = {
  backend?: string;
  ai_service?: string;
  error?: string;
};

function Home() {
  const [status, setStatus] = useState<HealthStatus | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const url = `${import.meta.env.VITE_BACKEND_URL}/health`;
    fetch(url)
      .then((r) => r.json())
      .then(setStatus)
      .catch((e) => setStatus({ error: String(e) }))
      .finally(() => setLoading(false));
  }, []);

  return (
    <div className="p-8">
      <div className="max-w-2xl mx-auto space-y-6">
        <header>
          <h1 className="text-3xl font-bold">agent-runner</h1>
          <p className="text-muted-foreground mt-1">Phase 0 — wiring check</p>
        </header>

        <section className="space-y-2">
          <h2 className="text-lg font-semibold">Service health</h2>
          <pre className="rounded-lg border bg-muted p-4 text-sm overflow-auto">
            {loading ? "checking..." : JSON.stringify(status, null, 2)}
          </pre>
        </section>
      </div>
    </div>
  );
}