# Multi-stage build: compile in a Go image, run in a minimal Alpine image.
# Final image is ~15MB instead of ~900MB.

FROM golang:1.25-alpine AS builder
WORKDIR /src

# Copy both the runner module and the gen/go module.
# The runner imports from gen/go, and go.work stitches them together.
COPY go.work ./
COPY apps/runner/go.mod apps/runner/go.sum* ./apps/runner/
COPY gen/go/go.mod gen/go/go.sum* ./gen/go/

# Download deps with workspace resolution.
RUN cd apps/runner && go mod download

# Copy the actual source.
COPY apps/runner/ ./apps/runner/
COPY gen/go/ ./gen/go/

# CGO_ENABLED=0 produces a static binary with no C dependencies —
# required for the tiny Alpine runtime stage below.
RUN cd apps/runner && CGO_ENABLED=0 go build -o /out/server ./cmd/server

# --- Runtime stage ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/server /usr/local/bin/server
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server"]
