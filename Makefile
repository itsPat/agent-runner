.PHONY: help setup proto up down logs clean backend-run ai-run frontend-run

# Default target: show help when you just type `make`
help:
	@echo "Common commands:"
	@echo "  make setup          Install deps across all subprojects"
	@echo "  make proto          Regenerate Go + TS code from .proto files"
	@echo "  make up             Start cockroach + backend + ai service (docker compose)"
	@echo "  make down           Stop all services"
	@echo "  make logs           Tail logs from all services"
	@echo "  make clean          Remove generated code and containers"
	@echo ""
	@echo "Dev commands (run services locally, outside docker, for fast iteration):"
	@echo "  make backend-run    Run backend locally (go run)"
	@echo "  make ai-run         Run ai service locally (bun dev)"
	@echo "  make frontend-run   Run frontend locally (bun dev)"

setup:
	@echo "→ Tidying Go modules..."
	cd backend && go mod tidy
	cd gen/go && go mod tidy
	@echo "→ Installing ai-service deps (bun)..."
	cd services/ai && bun install
	@echo "→ Installing frontend deps (bun)..."
	cd frontend && bun install || echo "  (skipping — frontend not scaffolded yet)"
	@echo "✓ Setup complete. Run 'make proto' next if you've changed .proto files."

proto:
	@echo "→ Generating protobuf code..."
	cd proto && buf generate
	@echo "✓ Generated code in gen/"

up:
	docker compose up -d
	@echo "✓ Services up. Check: curl http://localhost:8080/health"
	@echo "  Frontend: cd frontend && bun dev"

down:
	docker compose down

logs:
	docker compose logs -f

clean:
	docker compose down -v
	rm -rf gen/go/agent gen/ts/agent
	@echo "✓ Cleaned generated code and containers."

# --- Dev shortcuts (run without docker for fast iteration) ---

backend-run:
	cd backend && go run ./cmd/server

ai-run:
	cd services/ai && bun run dev

frontend-run:
	cd frontend && bun dev
