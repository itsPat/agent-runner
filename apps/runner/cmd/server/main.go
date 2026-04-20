package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/itsPat/agent-runner/apps/runner/internal/adapters/cockroach"
	"github.com/itsPat/agent-runner/apps/runner/internal/adapters/httpapi"
	"github.com/itsPat/agent-runner/apps/runner/internal/adapters/memeventbus"
	"github.com/itsPat/agent-runner/apps/runner/internal/app"
	agentv1 "github.com/itsPat/agent-runner/gen/go/agent/v1"
	"github.com/itsPat/agent-runner/gen/go/agent/v1/agentv1connect"
)

func main() {
	// Structured JSON logging — future-friendly for observability tools.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	aiServiceAddr := getenv("AI_SERVICE_ADDR", "localhost:8081")
	httpAddr := getenv("HTTP_ADDR", ":8080")
	cockroachDSN := getenv("COCKROACH_DSN", "postgres://root@localhost:26257/defaultdb?sslmode=disable")
	aiServiceBaseURL := normalizeBaseURL(aiServiceAddr)

	// --- Database: open pool, run migrations ---
	// Startup budget: pool open + ping + migrations must finish in 30s.
	// CRDB in docker-compose can be slow to accept connections right after
	// its healthcheck first reports healthy.
	startupCtx, startupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer startupCancel()

	pool, err := cockroach.NewPool(startupCtx, cockroachDSN)
	if err != nil {
		slog.Error("open cockroach pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := cockroach.RunMigrations(startupCtx, pool); err != nil {
		slog.Error("run migrations", "err", err)
		os.Exit(1)
	}
	slog.Info("migrations applied", "dsn", cockroachDSN)

	// The runner talks to the AI service over Connect on plain HTTP.
	aiClient := agentv1connect.NewAgentServiceClient(
		http.DefaultClient,
		aiServiceBaseURL,
	)

	// Prove the wiring: ping the AI service at startup.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()

	pingResp, err := pingAI(pingCtx, aiClient, "hello from runner")
	if err != nil {
		slog.Warn("initial ping to AI service failed (will keep trying via /health)", "err", err)
	} else {
		slog.Info("ping successful", "response", pingResp.Message, "server_time", pingResp.ServerTimeUnix)
	}

	// --- App layer: wire ports to use cases ---
	taskStore := cockroach.NewTaskStore(pool)
	eventBus := memeventbus.New()
	stubEmitter := app.NewStubEmitter(eventBus)
	runService := app.NewRunService(taskStore, stubEmitter)

	// --- HTTP server ---
	mux := http.NewServeMux()

	// Public API (POST /runs, GET /runs/:id) from the httpapi adapter.
	httpapi.NewRouter(runService).Register(mux)
	// SSE: GET /runs/:id/events
	httpapi.NewSSEHandler(eventBus).Register(mux)

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		// Check we can still reach the AI service.
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		_, err := pingAI(ctx, aiClient, "health")
		status := map[string]any{
			"runner": "ok",
			"ai":     "ok",
		}
		if err != nil {
			status["ai"] = "unreachable: " + err.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status)
	})

	srv := &http.Server{
		Addr:              httpAddr,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// --- Graceful shutdown ---
	// Preview of the pattern we'll lean on heavily in Phase 4.
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", httpAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		slog.Error("http server crashed", "err", err)
		os.Exit(1)
	case sig := <-stop:
		slog.Info("shutdown signal received", "signal", sig.String())
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	}
	slog.Info("bye")
}

func pingAI(ctx context.Context, client agentv1connect.AgentServiceClient, message string) (*agentv1.PingResponse, error) {
	return client.Ping(ctx, &agentv1.PingRequest{Message: message})
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func normalizeBaseURL(addr string) string {
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	return (&url.URL{Scheme: "http", Host: addr}).String()
}

// Dev-only CORS middleware. Good enough for localhost; tighten later.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
