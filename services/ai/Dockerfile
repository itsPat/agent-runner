FROM oven/bun:1.1-alpine
WORKDIR /app

# ai-service imports generated code from gen/ts, so we need both.
# The compose file sets context to the repo root for this reason.
COPY services/ai/package.json services/ai/bun.lockb* ./services/ai/
COPY gen/ts ./gen/ts

WORKDIR /app/services/ai
RUN bun install --frozen-lockfile || bun install

COPY services/ai/ ./

EXPOSE 8081
CMD ["bun", "run", "src/index.ts"]
