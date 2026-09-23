package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type InputSubmitter interface {
	CreateSession(context.Context, string, store.CreateSessionInput) (store.Session, error)
	SubmitInputs(context.Context, string, string, string, []store.Input) ([]store.InputReceipt, error)
}

type Option func(*Handler)

// WithExecution enables durable admission when the service owns an execution worker.
func WithExecution(s InputSubmitter) Option { return func(h *Handler) { h.inputs = s } }

// @Summary Submit Session input events
// @Description An empty events array is a resource-authorized no-op; it creates no execution retry identity, Turn, Item or input receipt. For environment none, atomically accepts text messages, cancellation and function results. Messages steer active work or start a queued Turn. The supported self_hosted profile accepts text-only messages; qualified Codex and Claude SDK openai_hosted profiles also accept inline PNG/JPEG. Under the Session lock, matching retries retain their original target; new active messages append to the current Turn, while idle messages reserve work and wait up to the original five-minute connection/admission deadline. Return 202 only after durable admission, without claiming native application; active messages create no Turn or reservation. Cancellation-only prepared-environment batches use existing durable cancellation admission and return 202 without waiting for native exit; a new cancellation conflicts while a pre-Turn reservation is pending. Homogeneous tool_result-only prepared-environment batches reuse existing scoped result admission and application receipts without creating a Turn or bypassing a pending reservation. Mixed prepared-environment batches remain unsupported. HTTP expiry/cancellation use local 409 environment_input_expired/environment_input_cancelled errors. New input on a Session whose hosted Environment failed to provision returns the observed 409 conflict_error "the hosted environment failed to provision"; input already waiting when it fails and expired Environments keep the local 409 environment_unavailable. Input the Session cannot accept in its current state, such as a result after cancellation or a batch while earlier input is pending, and a result that differs from the call's saved result return 409 with type and code conflict_error; reusing an Idempotency-Key with a different batch returns the local 409 idempotency_conflict. Inside an owned Session, a result for an unknown call or for a call of another Turn returns 400 invalid_request_error and changes nothing; missing and foreign Sessions return 404. Losing execution ownership returns 503. The response write deadline accommodates the admission window for either prepared Environment, independently of new-hosted-admission and executor URL settings. Disconnecting the waiting HTTP request does not cancel retained work or restart its deadline. Retry keys identify the whole ordered batch. Function output accepts text or ordered text/image parts subject to engine support; Claude SDK accepts text results and, on none and qualified openai_hosted, successful inline PNG/JPEG results, preserving ordered content; error images and remote references reject before admission. Native image resizing may change bytes. Runtime image-result support is checked only for image-bearing delivery. Codex and Claude SDK on none and qualified openai_hosted accept ordered inline PNG/JPEG image messages. Self-hosted profiles and other engines remain text-only; remote image URLs are unsupported. Image references are retained unchanged without service-side downloads.
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
	var request struct {
		Events []json.RawMessage `json:"events"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request exceeds 1 MiB.")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request", "Invalid Session input event request.")
		}
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request must contain exactly one JSON object.")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = uuid.NewString()
	}
	if err := store.ValidateInputKey(key); err != nil {
		writeStoreError(w, r, err)
		return
	}
	if request.Events != nil && len(request.Events) == 0 {
		// Empty batches have no execution identity to reserve or replay.
		// Authorize the resource even when no executor is configured.
		if _, err := h.store.GetSession(r.Context(), tenantID(r), chi.URLParam(r, "session_id")); err != nil {
			writeStoreError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if h.inputs == nil {
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
	if _, err := h.inputs.SubmitInputs(r.Context(), tenantID(r), sessionID, key, inputs); err != nil {
		writeInputError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
}

func executionInputs(events []json.RawMessage) ([]store.Input, error) {
	if len(events) == 0 || len(events) > 64 {
		return nil, store.ErrInvalidInput
	}
	inputs := make([]store.Input, 0, len(events))
	for _, raw := range events {
		event, err := decodeInputEvent(raw)
		if err != nil {
			return nil, err
		}
		switch event.Type {
		case "agent.session.input.cancel":
			if event.Input != nil {
				return nil, store.ErrInvalidInput
			}
			inputs = append(inputs, store.Input{Kind: "cancel", Payload: json.RawMessage(`{}`)})
		case "agent.session.input.tool_result":
			input, err := functionResultInput(event)
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, input)
		case "agent.session.input.message":
			if len(event.Input) == 0 {
				return nil, store.ErrInvalidInput
			}
			for _, message := range event.Input {
				if message.Role != "user" || (message.Type != "" && message.Type != "message") || len(message.Content) == 0 {
					return nil, store.ErrInvalidInput
				}
				converted := proto.MessageInput{{}}
				for _, content := range message.Content {
					converted[0].Content = append(converted[0].Content, proto.InputContent{Type: content.Type, Text: content.Text, ImageURL: content.ImageURL})
				}
				if converted.Validate() != nil || converted.ValidateInlineImages() != nil {
					return nil, store.ErrInvalidInput
				}
			}
			payload, err := json.Marshal(event)
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, store.Input{Kind: "message", Payload: payload})
		default:
			return nil, store.ErrInvalidInput
		}
	}
	return inputs, nil
}
