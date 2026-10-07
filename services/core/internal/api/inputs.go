package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// InputAdmission submits Session input through the execution Worker, which
// validates execution support first.
type InputAdmission interface {
	SubmitInputs(context.Context, string, string, string, []sessions.Input) ([]sessions.InputReceipt, error)
}

func (h *Handler) createEvents(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONObject(w, r)
	if !ok {
		return
	}
	var request struct {
		Events []json.RawMessage `json:"events"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	// An unknown member, including a case variant of events, is rejected before
	// decoding; see inexactMember.
	if inexactMember(raw, reflect.TypeOf(request)) || decoder.Decode(&request) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid Session input event request.")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = uuid.NewString()
	}
	if err := sessions.ValidateInputKey(key); err != nil {
		writeSessionsError(w, r, err)
		return
	}
	if request.Events != nil && len(request.Events) == 0 {
		// Empty batches have no execution identity to reserve or replay.
		// Authorize the resource even when no executor is configured.
		if _, err := h.SessionsReader.GetSession(r.Context(), tenantID(r), chi.URLParam(r, "session_id")); err != nil {
			writeSessionsError(w, r, err)
			return
		}
		if !h.auditSessionOperation(w, r, chi.URLParam(r, "session_id"), "send_events") {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if h.Execution == nil {
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Execution is not enabled on this service.")
		return
	}
	inputs, err := executionInputs(request.Events)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	sessionID := chi.URLParam(r, "session_id")
	if err := h.setEnvironmentInputWriteDeadline(w, r, sessionID); err != nil {
		writeSessionsError(w, r, err)
		return
	}
	if _, err := h.Execution.InputAdmission.SubmitInputs(r.Context(), tenantID(r), sessionID, key, inputs); err != nil {
		writeInputError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
}

func executionInputs(events []json.RawMessage) ([]sessions.Input, error) {
	if len(events) == 0 || len(events) > 64 {
		return nil, sessions.ErrInvalidInput
	}
	inputs := make([]sessions.Input, 0, len(events))
	for _, raw := range events {
		event, err := decodeInputEvent(raw)
		if err != nil {
			return nil, err
		}
		switch event.Type {
		case "agent.session.input.cancel":
			if event.Input != nil {
				return nil, sessions.ErrInvalidInput
			}
			inputs = append(inputs, sessions.Input{Kind: "cancel", Payload: json.RawMessage(`{}`)})
		case "agent.session.input.tool_result":
			input, err := functionResultInput(event)
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, input)
		case "agent.session.input.message":
			if len(event.Input) == 0 {
				return nil, sessions.ErrInvalidInput
			}
			for _, message := range event.Input {
				if message.Role != "user" || (message.Type != "" && message.Type != "message") || len(message.Content) == 0 {
					return nil, sessions.ErrInvalidInput
				}
				converted := proto.MessageInput{{}}
				for _, content := range message.Content {
					converted[0].Content = append(converted[0].Content, proto.InputContent{Type: content.Type, Text: content.Text, ImageURL: content.ImageURL})
				}
				if converted.Validate() != nil || converted.ValidateInlineImages() != nil {
					return nil, sessions.ErrInvalidInput
				}
			}
			payload, err := json.Marshal(event)
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, sessions.Input{Kind: "message", Payload: payload})
		default:
			return nil, sessions.ErrInvalidInput
		}
	}
	return inputs, nil
}
