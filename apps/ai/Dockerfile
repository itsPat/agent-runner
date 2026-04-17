FROM oven/bun:1.1-alpine
WORKDIR /app

# The ai app imports generated code from gen/ts, so we need both.
# The compose file sets context to the repo root for this reason.
COPY apps/ai/package.json apps/ai/bun.lock* ./apps/ai/
COPY gen/ts ./gen/ts

WORKDIR /app/apps/ai
RUN bun install --frozen-lockfile || bun install

COPY apps/ai/ ./

EXPOSE 8081
CMD ["bun", "run", "src/index.ts"]
