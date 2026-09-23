package api

import (
	"context"
	"encoding/json"
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type sessionStreamCreator interface {
	CreateSessionStream(context.Context, string, store.CreateSessionInput) (store.SessionCreation, error)
}

func (h *Handler) createSessionStream(w http.ResponseWriter, r *http.Request, input store.CreateSessionInput) {
	// Check every stream capability before the creation can commit.
	events, ok := h.store.(eventStore)
	snapshots, snapshotted := h.store.(sessionSnapshotStore)
	if !ok || !snapshotted {
		writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "Live events are unavailable.")
		return
	}
	var source any = h.store
	var config configuration
	if err := json.Unmarshal(input.Configuration, &config); err != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	if len(input.InitialInputs) > 0 || config.Environment.Type == "openai_hosted" {
		if h.inputs == nil {
			writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Execution input is not enabled on this service.")
			return
		}
		source = h.inputs
	}
	creator, ok := source.(sessionStreamCreator)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "Streaming creation is unavailable.")
		return
	}
	result, err := creator.CreateSessionStream(r.Context(), tenantID(r), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.respondSessionCreationStream(w, r, events, snapshots, result)
}

type sessionSnapshotStore interface {
	SessionStreamSnapshot(context.Context, string, string) (store.Session, int64, error)
}

// respondSessionCreationStream renders result.Session, the committed Session
// projection read after result.Cursor, which is also the JSON 201 body. A fresh
// creation sends it as agent.session.created, then streams changes after the
// cursor until the Session settles, or ends at once when nothing was admitted.
// A same-key retry of an existing creation admits nothing and sends no events;
// official same-key requests create distinct Sessions, so there is no retry
// stream to follow. Recover with stream=false or the GET events stream. Only a
// fresh creation uses snapshots.
func (h *Handler) respondSessionCreationStream(w http.ResponseWriter, r *http.Request, events eventStore, snapshots sessionSnapshotStore, result store.SessionCreation) {
	if !result.Created {
		openEventStream(w, http.StatusCreated)
		return
	}
	response, err := sessionResponse(result.Session, h.executorURL)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	created := v1.SessionEvent{Type: "agent.session.created", EventID: uuid.NewString(), Session: &response}
	if session := result.Session; sessionSettled(session, response) && session.LastTurn == nil && session.EnvironmentInputActivity == nil {
		// Nothing was admitted, e.g. self_hosted creation without input.
		if write := openEventStream(w, http.StatusCreated); write != nil {
			_ = emitSessionEvent(write, session.ID, created)
		}
		return
	}
	tenant, id := tenantID(r), result.Session.ID
	settlement := func(ctx context.Context) (bool, int64, error) {
		session, cursor, err := snapshots.SessionStreamSnapshot(ctx, tenant, id)
		if err != nil {
			return false, 0, err
		}
		response, err := sessionResponse(session, h.executorURL)
		return err == nil && sessionSettled(session, response), cursor, err
	}
	h.serveSessionEvents(w, r, events, result.Session, result.Cursor, &created, http.StatusCreated, settlement)
}

// sessionSettled reports that a committed projection has no admitted work left:
// the Session is idle or failed, its latest Turn is not queued, running or
// waiting, and its latest input reservation is not pending.
func sessionSettled(session store.Session, response v1.Session) bool {
	if response.Status != "idle" && response.Status != "failed" {
		return false
	}
	if turn := session.LastTurn; turn != nil {
		switch turn.Status {
		case store.TurnQueued, store.TurnInProgress, store.TurnWaiting:
			return false
		}
	}
	return !session.PendingInput
}
