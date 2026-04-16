package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	agentv1 "github.com/itsPat/agent-runner/gen/go/agent/v1"
)

func main() {
	// Structured JSON logging — future-friendly for observability tools.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	aiServiceAddr := getenv("AI_SERVICE_ADDR", "localhost:8081")
	httpAddr := getenv("HTTP_ADDR", ":8080")

	// --- Connect to AI service over gRPC ---
	// Insecure creds are fine for local dev.
	conn, err := grpc.NewClient(
		aiServiceAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		slog.Error("failed to create gRPC client", "err", err)
		os.Exit(1)
	}
	defer conn.Close()

	aiClient := agentv1.NewAgentServiceClient(conn)

	// Prove the wiring: ping the AI service at startup.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()

	pingResp, err := aiClient.Ping(pingCtx, &agentv1.PingRequest{Message: "hello from backend"})
	if err != nil {
		slog.Warn("initial ping to AI service failed (will keep trying via /health)", "err", err)
	} else {
		slog.Info("ping successful", "response", pingResp.Message, "server_time", pingResp.ServerTimeUnix)
	}

	// --- HTTP server ---
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		// Check we can still reach the AI service.
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		_, err := aiClient.Ping(ctx, &agentv1.PingRequest{Message: "health"})
		status := map[string]any{
			"backend":    "ok",
			"ai_service": "ok",
		}
		if err != nil {
			status["ai_service"] = "unreachable: " + err.Error()
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

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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