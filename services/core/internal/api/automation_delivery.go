package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/automations"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

const (
	// automationBatch bounds the deliveries one pass runs at once.
	automationBatch = 16
	// automationDeliveryTimeout bounds one delivery.
	automationDeliveryTimeout = 5 * time.Second
)

// deliverAutomations is one pass: it records the due occurrences and delivers
// the queued invocations. A delivery that cannot finish yet stays queued for
// the next pass. It reports what it recorded and settled, and counts only
// storage failures as failed; an invocation that failed for good is a settled
// record, not a job failure.
func (h *Handler) deliverAutomations(ctx context.Context) (*int64, int64, error) {
	var settled, failed atomic.Int64
	recorded, err := h.Automations.Record(ctx, time.Now())
	if err != nil {
		log.Ctx(ctx).Error("Automation occurrences could not be recorded", "error", err)
		failed.Add(1)
	}
	deliveries, err := h.Automations.Deliverable(ctx, automationBatch)
	if err != nil {
		return nil, failed.Load() + 1, err
	}
	var wg sync.WaitGroup
	for _, d := range deliveries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcome, done := h.deliverAutomation(ctx, d)
			if !done {
				return
			}
			if err := h.Automations.Settle(ctx, outcome); err != nil {
				log.Ctx(ctx).Error("Automation invocation could not be settled", "error", err)
				failed.Add(1)
				return
			}
			settled.Add(1)
		}()
	}
	wg.Wait()
	processed := int64(recorded) + settled.Load()
	if processed == 0 && failed.Load() == 0 {
		return nil, 0, nil
	}
	return &processed, failed.Load(), nil
}

// deliverAutomation runs one delivery through the code the public routes run.
// It returns the outcome to settle and false when the delivery stays queued.
func (h *Handler) deliverAutomation(ctx context.Context, d automations.Delivery) (automations.Outcome, bool) {
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, principalContextKey{}, d.Scope), automationDeliveryTimeout)
	defer cancel()
	response := httptest.NewRecorder()
	outcome := automations.Outcome{InvocationID: d.InvocationID, Status: "completed"}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/agents/sessions", bytes.NewReader(d.Request))
	if d.TargetType == "new_session" {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", d.InvocationID)
		h.createSession(response, request)
		if response.Code == http.StatusCreated {
			var session struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(response.Body.Bytes(), &session) != nil || session.ID == "" {
				return outcome, false
			}
			outcome.SessionID = session.ID
			return outcome, true
		}
	} else {
		var target struct {
			SessionID    string `json:"session_id"`
			Instructions string `json:"instructions"`
		}
		_ = json.Unmarshal(d.Request, &target)
		inputs, err := executionInputs([]json.RawMessage{instructionsEvent(target.Instructions)})
		if err == nil {
			_, err = h.Execution.InputAdmission.SubmitInputs(ctx, d.Scope.TenantID, target.SessionID, d.InvocationID, inputs)
		}
		if err == nil {
			outcome.SessionID = target.SessionID
			return outcome, true
		}
		if errors.Is(err, sessions.ErrTurnConflict) || errors.Is(err, execution.ErrExecutionUnavailable) || ctx.Err() != nil {
			return outcome, false
		}
		writeInputError(response, request, err)
	}
	var envelope struct {
		Error struct {
			Type    string  `json:"type"`
			Code    *string `json:"code"`
			Message string  `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &envelope)
	code := envelope.Error.Type
	if envelope.Error.Code != nil {
		code = *envelope.Error.Code
	}
	if response.Code == http.StatusServiceUnavailable && (code == "execution_unavailable" || code == "sandbox_reset_in_progress") || ctx.Err() != nil {
		return outcome, false
	}
	outcome.Status, outcome.Code, outcome.Message = "failed", code, envelope.Error.Message
	if outcome.Code == "" {
		outcome.Code, outcome.Message = "internal_error", "The delivery could not be completed."
	}
	return outcome, true
}
