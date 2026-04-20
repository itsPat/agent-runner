import { create } from "@bufbuild/protobuf";
import type { ConnectRouter } from "@connectrpc/connect";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { createServer } from "node:http";

import { AgentService } from "@gen/agent/v1/service_pb.js";
import {
  PingResponseSchema,
} from "@gen/agent/v1/service_pb.js";

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
