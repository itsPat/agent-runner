import * as grpc from "@grpc/grpc-js";
import { create, toBinary, fromBinary } from "@bufbuild/protobuf";
import {
  PingRequestSchema,
  PingResponseSchema,
} from "@gen/agent/v1/service_pb.js";

const PORT = process.env.GRPC_PORT ?? "8081";

// -----------------------------------------------------------------------------
// gRPC service definition
// -----------------------------------------------------------------------------
// @grpc/grpc-js is "pure" gRPC — it needs raw serialize/deserialize functions
// because it doesn't know about protobuf at all. @bufbuild/protobuf gives us
// `toBinary` and `fromBinary` which turn messages into Uint8Arrays and back.
//
// We wire them together manually below. In Phase 2+ we may switch to a higher-
// level framework (like @connectrpc/connect) that handles this automatically,
// but doing it by hand once is educational.
// -----------------------------------------------------------------------------

const agentServiceDefinition: grpc.ServiceDefinition = {
  ping: {
    path: "/agent.v1.AgentService/Ping",
    requestStream: false,
    responseStream: false,
    requestSerialize: (req: unknown) =>
      Buffer.from(toBinary(PingRequestSchema, req as any)),
    requestDeserialize: (buf: Buffer) =>
      fromBinary(PingRequestSchema, new Uint8Array(buf)),
    responseSerialize: (res: unknown) =>
      Buffer.from(toBinary(PingResponseSchema, res as any)),
    responseDeserialize: (buf: Buffer) =>
      fromBinary(PingResponseSchema, new Uint8Array(buf)),
  },
};

// -----------------------------------------------------------------------------
// Handlers
// -----------------------------------------------------------------------------

function pingHandler(
  call: grpc.ServerUnaryCall<any, any>,
  callback: grpc.sendUnaryData<any>,
) {
  const msg = call.request?.message ?? "<no message>";
  console.log(`[ai-service] ping received: "${msg}"`);

  const response = create(PingResponseSchema, {
    message: `pong (echo: ${msg})`,
    serverTimeUnix: BigInt(Math.floor(Date.now() / 1000)),
  });

  callback(null, response);
}

// -----------------------------------------------------------------------------
// Server bootstrap
// -----------------------------------------------------------------------------

function main() {
  const server = new grpc.Server();

  server.addService(agentServiceDefinition, {
    ping: pingHandler,
  });

  server.bindAsync(
    `0.0.0.0:${PORT}`,
    grpc.ServerCredentials.createInsecure(),
    (err, port) => {
      if (err) {
        console.error("[ai-service] failed to bind:", err);
        process.exit(1);
      }
      console.log(`[ai-service] gRPC listening on :${port}`);
    },
  );

  // Graceful shutdown
  const shutdown = () => {
    console.log("[ai-service] shutting down");
    server.tryShutdown(() => process.exit(0));
    setTimeout(() => process.exit(1), 5000).unref();
  };
  process.on("SIGINT", shutdown);
  process.on("SIGTERM", shutdown);
}

main();
