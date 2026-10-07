package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/go-chi/chi/v5"
)

// Turns reads a Session's root Turns.
type Turns interface {
	GetTurn(context.Context, string, string, string) (sessions.Turn, error)
	ListTurns(context.Context, string, string, string, int, bool) (sessions.TurnPage, error)
}

func (h *Handler) getTurn(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")
	turn, err := h.Turns.GetTurn(r.Context(), tenantID(r), sessionID, chi.URLParam(r, "turn_id"))
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	session, err := h.SessionsReader.GetSession(r.Context(), tenantID(r), sessionID)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	response, err := turnResponse(session, turn)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) listTurns(w http.ResponseWriter, r *http.Request) {
	options, ok := readPage(w, r)
	if !ok {
		return
	}
	sessionID := chi.URLParam(r, "session_id")
	session, err := h.SessionsReader.GetSession(r.Context(), tenantID(r), sessionID)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	page, err := h.Turns.ListTurns(r.Context(), tenantID(r), sessionID, options.after, options.limit, options.ascending)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	response := v1.TurnList{Data: make([]v1.Turn, 0, len(page.Turns)), HasMore: page.NextCursor != ""}
	for _, turn := range page.Turns {
		item, err := turnResponse(session, turn)
		if err != nil {
			writeSessionsError(w, r, err)
			return
		}
		response.Data = append(response.Data, item)
	}
	writeJSON(w, http.StatusOK, turnListResponse(response.Data, response.HasMore))
}

func turnResponse(session sessions.Session, turn sessions.Turn) (v1.Turn, error) {
	var cfg configuration
	if err := json.Unmarshal(session.Configuration, &cfg); err != nil || cfg.Agent.ID == "" {
		return v1.Turn{}, errors.New("missing stored agent identity")
	}
	// Session Turns are root Turns, so subagent_id is always null here.
	response := v1.Turn{Usage: tokenUsage(turn.Usage), ID: turn.ID, SessionID: turn.SessionID, AgentID: cfg.Agent.ID, Object: "agent.session.turn", Status: turn.Status, CreatedAt: turn.CreatedAt.Unix(), StartedAt: unixTime(turn.StartedAt), CompletedAt: unixTime(turn.CompletedAt)}
	if turn.Status == sessions.TurnFailed {
		// Native errors can contain secrets; publish a stable category without raw diagnostics.
		response.Error = &v1.TurnError{Code: "internal_error", Message: "The execution could not complete."}
	}
	return response, nil
}

func unixTime(value time.Time) *int64 {
	if value.IsZero() {
		return nil
	}
	seconds := value.Unix()
	return &seconds
}
