package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/itsPat/agent-runner/apps/runner/internal/domain"
	"github.com/itsPat/agent-runner/apps/runner/internal/ports"
)

// heartbeatInterval keeps idle SSE connections alive across proxies and
// sleepy laptops. Sent as a comment line — the browser ignores it.
const heartbeatInterval = 25 * time.Second

// eventDTO is the SSE wire shape. It lives here, not in domain, because
// "what goes over HTTP" is an adapter concern.
type eventDTO struct {
	ID        uuid.UUID       `json:"id"`
	RunID     uuid.UUID       `json:"run_id"`
	TaskID    *uuid.UUID      `json:"task_id,omitempty"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

func eventToDTO(e domain.Event) eventDTO {
	return eventDTO{
		ID:        e.ID,
		RunID:     e.RunID,
		TaskID:    e.TaskID,
		Kind:      string(e.Kind),
		Payload:   e.Payload,
		CreatedAt: e.CreatedAt,
	}
}

// SSEHandler streams events for a single run to an HTTP client using the
// Server-Sent Events protocol. One goroutine per connected client.
type SSEHandler struct {
	bus ports.EventBus
}

func NewSSEHandler(bus ports.EventBus) *SSEHandler {
	return &SSEHandler{bus: bus}
}

// Register attaches GET /runs/{id}/events to the given mux.
func (h *SSEHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /runs/{id}/events", h.serve)
}

func (h *SSEHandler) serve(w http.ResponseWriter, r *http.Request) {
	runID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}

	// SSE requires a flusher; fail fast if the writer cannot do one.
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// SSE headers. Set before the first Flush.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx etc.)
	w.WriteHeader(http.StatusOK)
	flusher.Flush() // tell the client the stream is open

	// Subscribe using the request context — cancellation on client
	// disconnect flows down to the bus and closes our channel.
	events := h.bus.Subscribe(r.Context(), runID)

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	slog.Info("sse client connected", "run_id", runID)
	defer slog.Info("sse client disconnected", "run_id", runID)

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-events:
			if !ok {
				// Bus closed the channel (ctx cancelled on its side).
				return
			}
			if err := writeSSEEvent(w, ev); err != nil {
				slog.Warn("sse write failed", "err", err, "run_id", runID)
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSEEvent serializes one event as a single SSE message. The "event:"
// line lets the browser's EventSource dispatch to named listeners; data
// is one JSON blob.
func writeSSEEvent(w http.ResponseWriter, e domain.Event) error {
	body, err := json.Marshal(eventToDTO(e))
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	// Each SSE message ends with a blank line.
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Kind, body)
	return err
}

