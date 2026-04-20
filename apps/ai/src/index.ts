import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import type { ConnectRouter } from "@connectrpc/connect";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { createServer } from "node:http";

import { AgentService } from "@gen/agent/v1/service_pb.js";
import {
  PingResponseSchema,
  PlanGoalResponseSchema,
  PlannedTaskSchema,
} from "@gen/agent/v1/service_pb.js";

import { planGoal } from "./planner.js";

const PORT = Number(process.env.PORT ?? "8081");

function routes(router: ConnectRouter) {
  router.service(AgentService, {
    async ping(req) {
      const message = req.message || "<no message>";
      console.log(`[ai] ping received: "${message}"`);

      return create(PingResponseSchema, {
        message: `pong (echo: ${message})`,
        serverTimeUnix: BigInt(Math.floor(Date.now() / 1000)),
      });
    },

    async planGoal(req) {
      const goal = req.goal.trim();
      console.log(`[ai] plan requested: "${goal}"`);
      if (!goal) {
        throw new ConnectError("goal is required", Code.InvalidArgument);
      }

      try {
        const plan = await planGoal(goal);
        console.log(
          `[ai] plan returned ${plan.tasks.length} tasks:`,
          JSON.stringify(
            plan.tasks.map((t) => ({
              name: t.name,
              kind: t.kind,
              deps: t.depends_on,
              spec: t.spec,
            })),
          ),
        );
        return create(PlanGoalResponseSchema, {
          tasks: plan.tasks.map((t) =>
            create(PlannedTaskSchema, {
              name: t.name,
              kind: t.kind,
              specJson: JSON.stringify(t.spec),
              dependsOn: t.depends_on,
            }),
          ),
        });
      } catch (err) {
        console.error("[ai] plan failed:", err);
        // ConnectError lets the Go side see a typed gRPC-like code rather
        // than a generic "internal" for expected validation failures.
        const msg = err instanceof Error ? err.message : String(err);
        throw new ConnectError(msg, Code.Internal);
      }
    },
  });
}

async function main() {
  const server = createServer(connectNodeAdapter({ routes }));

  server.listen(PORT, "0.0.0.0", () => {
    console.log(`[ai] Connect listening on :${PORT}`);
  });

  const shutdown = () => {
    console.log("[ai] shutting down");
    server.close((error) => {
      if (error) {
        console.error("[ai] shutdown failed:", error);
        process.exit(1);
      }
      process.exit(0);
    });
    setTimeout(() => process.exit(1), 5000).unref();
  };

  process.on("SIGINT", shutdown);
  process.on("SIGTERM", shutdown);
}

void main();
