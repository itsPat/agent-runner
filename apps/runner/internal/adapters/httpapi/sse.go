package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
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
	Seq       int64           `json:"seq"`
	RunID     uuid.UUID       `json:"run_id"`
	TaskID    *uuid.UUID      `json:"task_id,omitempty"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

func eventToDTO(e domain.Event) eventDTO {
	return eventDTO{
		ID:        e.ID,
		Seq:       e.Seq,
		RunID:     e.RunID,
		TaskID:    e.TaskID,
		Kind:      string(e.Kind),
		Payload:   e.Payload,
		CreatedAt: e.CreatedAt,
	}
}

// SSEHandler streams events for a single run to an HTTP client using the
// Server-Sent Events protocol. One goroutine per connected client.
//
// The handler combines durable history (EventStore) with live pub/sub
// (EventBus) so a late subscriber still sees every event that was ever
// published for the run. The sequence is subscribe-first, replay-history,
// then live-with-dedup — the dedup is what closes the gap between the
// history query completing and the live feed taking over.
type SSEHandler struct {
	events ports.EventStore
	bus    ports.EventBus
}

func NewSSEHandler(events ports.EventStore, bus ports.EventBus) *SSEHandler {
	return &SSEHandler{events: events, bus: bus}
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

	// Cursor comes from the SSE-native Last-Event-ID header. Browsers'
	// EventSource sets this automatically on reconnect. Empty/malformed
	// means "start from zero" — the client gets the full history.
	cursor := parseCursor(r.Header.Get("Last-Event-ID"))

	// Subscribe FIRST, before reading history. Any event published while
	// we query the DB lands in the subscriber's buffer and gets deduped
	// on the way out. Flipping this order would leave a gap.
	live := h.bus.Subscribe(r.Context(), runID)

	// SSE headers. Set before the first Flush.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx etc.)
	w.WriteHeader(http.StatusOK)
	flusher.Flush() // tell the client the stream is open

	slog.Info("sse client connected", "run_id", runID, "cursor", cursor)
	defer slog.Info("sse client disconnected", "run_id", runID)

	// --- Replay history ---
	history, err := h.events.ListSince(r.Context(), runID, cursor)
	if err != nil {
		slog.Error("sse history fetch failed", "err", err, "run_id", runID)
		return
	}
	var maxSent int64 = cursor
	for _, ev := range history {
		if err := writeSSEEvent(w, ev); err != nil {
			return
		}
		if ev.Seq > maxSent {
			maxSent = ev.Seq
		}
	}
	flusher.Flush()

	// --- Live feed with dedup ---
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-live:
			if !ok {
				// Bus closed the channel (ctx cancelled on its side).
				return
			}
			// Skip anything already covered by the replay. Seq == 0 means
			// the event was not persisted — publish anyway, it carries no
			// cursor weight.
			if ev.Seq != 0 && ev.Seq <= maxSent {
				continue
			}
			if err := writeSSEEvent(w, ev); err != nil {
				slog.Warn("sse write failed", "err", err, "run_id", runID)
				return
			}
			if ev.Seq > maxSent {
				maxSent = ev.Seq
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

func parseCursor(header string) int64 {
	if header == "" {
		return 0
	}
	n, err := strconv.ParseInt(header, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// writeSSEEvent serializes one event as a single SSE message.
//
//   - "id: <seq>" tells the browser's EventSource to remember this as
//     the last-event-id; it will send it back on the next reconnect.
//   - "event: <kind>" lets EventSource.addEventListener(kind, handler)
//     dispatch to named listeners instead of a catch-all "message".
//   - "data: <json>" is the payload. JSON must be single-line (no
//     newlines) or split into multiple "data:" lines; json.Marshal gives
//     us single-line by default.
//
// Each SSE message ends with a blank line.
func writeSSEEvent(w http.ResponseWriter, e domain.Event) error {
	body, err := json.Marshal(eventToDTO(e))
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	if e.Seq > 0 {
		_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Kind, body)
	} else {
		_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Kind, body)
	}
	return err
}

