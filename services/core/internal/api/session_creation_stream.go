package api

import (
	"context"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

// respondSessionCreationStream renders result.Session, the committed Session
// projection read after result.Cursor, which is also the JSON 201 body. A fresh
// creation sends it as agent.session.created, then streams changes after the
// cursor until the Session settles, or ends at once when nothing was admitted.
// A same-key retry of an existing creation admits nothing and sends no events;
// official same-key requests create distinct Sessions, so there is no retry
// stream to follow. Recover with stream=false or the GET events stream. Only a
// fresh creation uses snapshots.
func (h *Handler) respondSessionCreationStream(w http.ResponseWriter, r *http.Request, result sessions.Creation) {
	if !result.Created {
		openEventStream(w, http.StatusCreated)
		return
	}
	response, err := sessionResponse(result.Session, h.executorURL())
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	created := v1.SessionEvent{Type: "agent.session.created", EventID: uuid.NewString(), Session: &response}
	if err := h.addSessionInstallation(w, r, &response); err != nil {
		writeSessionsError(w, r, err)
		return
	}
	if session := result.Session; sessionSettled(session, response) && session.LastTurn == nil && session.EnvironmentInputActivity == nil {
		// Nothing was admitted, e.g. self_hosted creation without input.
		if write := openEventStream(w, http.StatusCreated); write != nil {
			_ = emitSessionEvent(write, session.ID, created)
		}
		return
	}
	tenant, id := tenantID(r), result.Session.ID
	settlement := func(ctx context.Context) (bool, int64, error) {
		session, cursor, err := h.SessionEvents.SessionStreamSnapshot(ctx, tenant, id)
		if err != nil {
			return false, 0, err
		}
		response, err := sessionResponse(session, h.executorURL())
		return err == nil && sessionSettled(session, response), cursor, err
	}
	h.serveSessionEvents(w, r, result.Session, result.Cursor, &created, http.StatusCreated, settlement)
}

// sessionSettled reports that a committed projection has no admitted work left:
// the Session is idle or failed, its latest Turn is not queued, running or
// waiting, and its latest input reservation is not pending.
func sessionSettled(session sessions.Session, response v1.Session) bool {
	if response.Status != "idle" && response.Status != "failed" {
		return false
	}
	if turn := session.LastTurn; turn != nil {
		switch turn.Status {
		case sessions.TurnQueued, sessions.TurnInProgress, sessions.TurnWaiting:
			return false
		}
	}
	return !session.PendingInput
}
