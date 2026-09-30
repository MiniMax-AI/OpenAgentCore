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

// @Summary Submit Session input events
// @Description An empty events array is a resource-authorized no-op; it creates no execution retry identity, Turn, Item or input receipt. For environment none, atomically accepts text messages, cancellation and function results. Messages steer active work or start a queued Turn. Qualified Codex and Claude SDK workspace profiles accept text and inline PNG/JPEG messages, independently of managed or self_hosted ownership. Under the Session lock, matching retries retain their original target; new active messages append to the current Turn, while idle messages reserve work and wait up to the original five-minute connection/admission deadline. Return 202 only after durable admission, without claiming native application; active messages create no Turn or reservation. Cancellation-only prepared-environment batches use existing durable cancellation admission and return 202 without waiting for native exit; a new cancellation conflicts while a pre-Turn reservation is pending. Homogeneous tool_result-only prepared-environment batches reuse existing scoped result admission and application receipts without creating a Turn or bypassing a pending reservation. Mixed prepared-environment batches remain unsupported. HTTP expiry/cancellation use local 409 environment_input_expired/environment_input_cancelled errors. New input on a Session whose hosted Environment failed to provision returns the observed 409 conflict_error "the hosted environment failed to provision"; input already waiting when it fails and expired Environments keep the local 409 environment_unavailable. Input the Session cannot accept in its current state, such as a result after cancellation or a batch while earlier input is pending, and a result that differs from the call's saved result return 409 with type and code conflict_error; reusing an Idempotency-Key with a different batch returns the local 409 idempotency_conflict. Inside an owned Session, a result for an unknown call or for a call of another Turn returns 400 invalid_request_error and changes nothing; missing and foreign Sessions return 404. Losing execution ownership returns 503. The response write deadline accommodates the admission window for either prepared Environment, independently of new-hosted-admission and executor URL settings. Disconnecting the waiting HTTP request does not cancel retained work or restart its deadline. Retry keys identify the whole ordered batch. Function output accepts text or ordered text/image parts subject to engine support; Claude SDK accepts text results and, on none and qualified workspace profiles, successful inline PNG/JPEG results, preserving ordered content; error images and remote references reject before admission. Native image resizing may change bytes. Runtime image-result support is checked only for image-bearing delivery. Codex and Claude SDK on none and qualified managed or self_hosted workspace profiles accept ordered inline PNG/JPEG image messages. Other engines remain text-only; remote image URLs are unsupported. Image references are retained unchanged without service-side downloads.
// @Tags Sessions
// @Accept json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param Idempotency-Key header string false "Retry key, up to 128 bytes"
// @Param session_id path string true "Session ID"
// @Param body body v1.CreateEventsRequest true "Ordered input events"
// @Success 202
// @Failure 400,401,404,409,413,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/events [post]
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
		writeStoreError(w, r, err)
		return
	}
	if request.Events != nil && len(request.Events) == 0 {
		// Empty batches have no execution identity to reserve or replay.
		// Authorize the resource even when no executor is configured.
		if _, err := h.Sessions.GetSession(r.Context(), tenantID(r), chi.URLParam(r, "session_id")); err != nil {
			writeStoreError(w, r, err)
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
		writeStoreError(w, r, err)
		return
	}
	sessionID := chi.URLParam(r, "session_id")
	if err := h.setEnvironmentInputWriteDeadline(w, r, sessionID); err != nil {
		writeStoreError(w, r, err)
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
