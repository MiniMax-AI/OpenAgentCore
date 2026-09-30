package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type eventStore interface {
	SessionEventCursor(context.Context, string, string) (int64, error)
	ListSessionEvents(context.Context, string, string, int64) ([]store.SessionChange, error)
}

// @Summary Stream live Session events
// @Description Live-only events, including command output fragments from capable Codex peers as agent.output.command_execution_output.delta with stable Item/output indexes. Native text conversion and output quotas apply; completion snapshots remain authoritative. Reconnect through Session, Turn and Items reads; missed events are not replayed. A lagging stream closes with an error when its bounded buffer is exceeded. When a hosted Environment fails to provision, the stream sends agent.session.environment.failed, an error event (environment_error/sandbox_error with the safe step and exit-status reason, never command output) and agent.session.failed, then ends. Session activity includes immutable pending-input connection actions before Turn creation; self_hosted environments use the same safe output as Session retrieval.
// @Description Active streams revalidate the original Project key every second before output; revocation, Project archival or authentication unavailability closes the stream. Authentication checks use a five-second timeout.
// @Tags Events
// @Produce text/event-stream
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Success 200 {object} v1.SessionEvent
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/events [get]
func (h *Handler) streamEvents(w http.ResponseWriter, r *http.Request) {
	events, ok := h.store.(eventStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "Live events are unavailable.")
		return
	}
	id, tenant := chi.URLParam(r, "session_id"), tenantID(r)
	session, err := h.store.GetSession(r.Context(), tenant, id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if _, err = sessionResponse(session, h.executorURL); err != nil {
		writeStoreError(w, r, err)
		return
	}
	cursor, err := events.SessionEventCursor(r.Context(), tenant, id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.serveSessionEvents(w, r, events, session, cursor, nil, http.StatusOK, nil)
}

// streamSettlement reads a creation stream's committed Session projection and
// the Session event cursor from one snapshot, reporting whether it is settled.
type streamSettlement func(context.Context) (settled bool, cursor int64, err error)

// serveSessionEvents streams committed changes after cursor. GET streams pass a
// nil settlement and stay live-only until disconnect, deletion or failure.
//
// A fresh creation response ends right after it sends a settling Session event
// and never sends later events: an agent.session.idle recorded when a Turn ends
// or an input reservation stops being pending, or any agent.session.failed.
// Settlements that record no event, such as a reservation cancelled while its
// Session is already idle, use the projection: after an empty drain the stream
// reads the projection and the event cursor from one snapshot and, if settled,
// sends only events up to that cursor before ending. Later work drained before
// that read can still be sent. The projection is re-read after a sent Session
// status event and otherwise at most once a second.
func (h *Handler) serveSessionEvents(w http.ResponseWriter, r *http.Request, events eventStore, session store.Session, cursor int64, initial *v1.SessionEvent, status int, settlement streamSettlement) {
	id, tenant := session.ID, tenantID(r)
	write := openEventStream(w, status)
	if write == nil {
		return
	}
	principal := r.Context().Value(principalContextKey{}).(identity.Principal)
	var checkedAuthority time.Time
	authorized := func() bool {
		if time.Since(checkedAuthority) < time.Second {
			return true
		}
		current, ok, err := h.resolvePrincipal(r)
		checkedAuthority = time.Now()
		return err == nil && ok && current == principal
	}
	// Recheck on idle polls and before output, including a continuously busy drain.
	// The existing resolver bounds authentication calls and fails closed.
	streamWrite := write
	write = func(data []byte) error {
		if !authorized() {
			return errors.New("stream authority is no longer available")
		}
		return streamWrite(data)
	}
	emit := func(event v1.SessionEvent) error { return emitSessionEvent(write, id, event) }
	if initial != nil {
		if err := emit(*initial); err != nil {
			return
		}
	}
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	// limit is the snapshot cursor of a settled projection; nothing after it is sent.
	recheck, limit := true, int64(-1)
	var checked time.Time
	for {
		if !authorized() {
			return
		}
		changes, err := events.ListSessionEvents(r.Context(), tenant, id, cursor)
		if errors.Is(err, store.ErrNotFound) {
			return
		}
		if err != nil {
			writeStreamFailure(write, id)
			return
		}
		for _, change := range changes {
			if limit >= 0 && change.Sequence > limit {
				return
			}
			event, err := streamResponse(session, change, h.executorURL)
			if err != nil {
				writeStreamFailure(write, id)
				return
			}
			if err := emit(event); err != nil {
				return
			}
			cursor = change.Sequence
			if terminalEvent(change) || settlement != nil && settlingEvent(change) {
				return
			}
			recheck = recheck || sessionStatusEvent(change.Event.Type)
		}
		if limit >= 0 && (cursor >= limit || len(changes) == 0) {
			return
		}
		if len(changes) > 0 {
			continue
		}
		if settlement != nil && (recheck || time.Since(checked) >= time.Second) {
			recheck, checked = false, time.Now()
			settled, snapshot, err := settlement(r.Context())
			if errors.Is(err, store.ErrNotFound) || r.Context().Err() != nil {
				return
			}
			if err != nil {
				writeStreamFailure(write, id)
				return
			}
			if settled {
				if cursor >= snapshot {
					return
				}
				limit = snapshot
				continue
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
		case <-heartbeat.C:
			if err := write([]byte(": keepalive\n\n")); err != nil {
				return
			}
		}
	}
}

// openEventStream writes the event-stream headers and the connection comment. It
// returns nil when that write fails.
func openEventStream(w http.ResponseWriter, status int) func([]byte) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(status)
	controller := http.NewResponseController(w)
	write := func(data []byte) error {
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		return controller.Flush()
	}
	if write([]byte(": connected\n\n")) != nil {
		return nil
	}
	return write
}

func emitSessionEvent(write func([]byte) error, session string, event v1.SessionEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		writeStreamFailure(write, session)
		return err
	}
	return write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event.Type, payload)))
}

// settlingEvent reports a recorded event after which a creation stream ends: an
// idle marked settled when recorded, or any failure. A self-hosted connection
// that clears a pending input's action to idle is not marked.
func settlingEvent(change store.SessionChange) bool {
	switch change.Event.Type {
	case "agent.session.failed":
		return true
	case "agent.session.idle":
		return change.Settled
	}
	return false
}

// terminalEvent reports the agent.session.failed of a hosted provisioning
// failure. The Session can never run again, so GET streams end after it too, as
// officially observed; other failures leave GET streams open.
func terminalEvent(change store.SessionChange) bool {
	return change.Event.Type == "agent.session.failed" && change.EnvironmentFailure != nil
}

func sessionStatusEvent(eventType string) bool {
	switch eventType {
	case "agent.session.in_progress", "agent.session.requires_action", "agent.session.idle", "agent.session.failed":
		return true
	}
	return false
}

func streamResponse(session store.Session, change store.SessionChange, executorURL string) (v1.SessionEvent, error) {
	event := change.Event
	if change.Turn == nil && change.EnvironmentInputActivity == nil && change.EnvironmentFailure == nil {
		return withTurnUsage(event), nil
	}
	if change.Turn != nil && strings.HasPrefix(event.Type, "agent.session.turn.") {
		turn, err := turnResponse(session, *change.Turn)
		event.Turn = &turn
		return withTurnUsage(event), err
	}
	event.SessionID = ""
	session.RequiredActions = change.RequiredActions
	session.LastTurn, session.Usage = change.Turn, change.SessionUsage
	session.EnvironmentInputActivity, session.EnvironmentFailure = change.EnvironmentInputActivity, change.EnvironmentFailure
	value, err := sessionResponse(session, executorURL)
	event.Session = &value
	return event, err
}

// withTurnUsage mirrors a terminal Turn snapshot's usage at the event's top
// level, including null when unknown. It never derives or sums counters.
func withTurnUsage(event v1.SessionEvent) v1.SessionEvent {
	event.Usage = nil
	if v1.TerminalTurnEvent(event.Type) && event.Turn != nil {
		event.Usage = event.Turn.Usage
	}
	return event
}

// writeStreamFailure sends Core's own interruption frame. Unlike official error
// events it omits param: released clients validate exactly code, type and message.
func writeStreamFailure(write func([]byte) error, session string) {
	event := struct {
		Type      string         `json:"type"`
		EventID   string         `json:"event_id"`
		SessionID string         `json:"session_id"`
		Error     v1.StreamError `json:"error"`
	}{"error", uuid.NewString(), session,
		v1.StreamError{Code: "stream_interrupted", Type: "server_error", Message: "The live stream was interrupted. Reconnect and retrieve the Session and its saved Items to recover."}}
	payload, _ := json.Marshal(event)
	_ = write([]byte(fmt.Sprintf("event: error\ndata: %s\n\n", payload)))
}
