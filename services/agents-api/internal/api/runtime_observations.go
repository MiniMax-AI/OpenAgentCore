package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"sync"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/go-chi/chi/v5"
)

const (
	runtimeObservationConcurrency   = 8
	runtimeObservationSourceBudget  = 2 * time.Second
	runtimeObservationRequestBudget = 10 * time.Second
)

var runtimeProviderTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

type RuntimeObservationService interface {
	ObserveSession(context.Context, string, string) (runtimeobs.Observation, error)
}

func WithRuntimeObservations(service RuntimeObservationService) Option {
	return func(h *Handler) { h.runtimeObservations = service }
}

// @Summary Retrieve a Session Runtime observation
// @Description Core extension returning one tenant-scoped, read-only current Runtime observation. It never provisions, renews, restarts, pauses or stops compute.
// @Tags Runtime observations
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Success 200 {object} v1.RuntimeObservation
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/runtime-observation [get]
func (h *Handler) getRuntimeObservation(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Runtime observation retrieval does not accept query parameters.")
		return
	}
	if h.runtimeObservations == nil {
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Runtime observation is not configured on this service.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), runtimeObservationSourceBudget)
	defer cancel()
	observation, err := h.runtimeObservations.ObserveSession(ctx, tenantID(r), chi.URLParam(r, "session_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response, err := runtimeObservationResponse(observation)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// @Summary List current Runtime observations
// @Description Core extension listing one current Runtime context per tenant-owned Session in Session creation order. Each row has an independent resolved_at and optional provider observed_at; the page is not an atomic telemetry snapshot.
// @Tags Runtime observations
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param after query string false "Last observation ID from the previous page"
// @Param limit query int false "Page size" minimum(1) maximum(100) default(20)
// @Param order query string false "Session creation order" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.RuntimeObservationList
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/runtime-observations [get]
func (h *Handler) listRuntimeObservations(w http.ResponseWriter, r *http.Request) {
	if h.runtimeObservations == nil {
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Runtime observation is not configured on this service.")
		return
	}
	options, ok := readPage(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), runtimeObservationRequestBudget)
	defer cancel()
	page, err := h.store.ListSessions(ctx, tenantID(r), options.after, options.limit, options.ascending, nil)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	observations := make([]runtimeobs.Observation, len(page.Sessions))
	semaphore := make(chan struct{}, runtimeObservationConcurrency)
	work, stop := context.WithCancel(ctx)
	defer stop()
	var wait sync.WaitGroup
	var once sync.Once
	var firstErr error
	for index, session := range page.Sessions {
		wait.Add(1)
		go func(index int, sessionID string) {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-work.Done():
				return
			}
			sampleCtx, sampleCancel := context.WithTimeout(work, runtimeObservationSourceBudget)
			defer sampleCancel()
			value, err := h.runtimeObservations.ObserveSession(sampleCtx, tenantID(r), sessionID)
			if err != nil {
				once.Do(func() { firstErr = err; stop() })
				return
			}
			observations[index] = value
		}(index, session.ID)
	}
	wait.Wait()
	if firstErr != nil {
		writeStoreError(w, r, firstErr)
		return
	}
	if err := ctx.Err(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Runtime observation collection exceeded its request budget.")
		return
	}
	response := v1.RuntimeObservationList{Object: "list", Data: make([]v1.RuntimeObservation, 0, len(observations)), HasMore: page.NextCursor != ""}
	for _, observation := range observations {
		item, err := runtimeObservationResponse(observation)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		response.Data = append(response.Data, item)
	}
	if len(response.Data) > 0 {
		response.FirstID = &response.Data[0].ID
		response.LastID = &response.Data[len(response.Data)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}

func runtimeObservationResponse(observation runtimeobs.Observation) (v1.RuntimeObservation, error) {
	if observation.Target.SessionID == "" || observation.ResolvedAt.IsZero() || observation.ResolvedAt.Unix() < 0 {
		return v1.RuntimeObservation{}, errors.New("invalid Runtime observation identity")
	}
	if !observation.Target.Instance.AllocationCreatedAt.IsZero() &&
		(observation.Target.Instance.AllocationCreatedAt.Unix() < 0 || observation.Target.Instance.AllocationCreatedAt.After(observation.ResolvedAt)) {
		return v1.RuntimeObservation{}, errors.New("invalid Runtime allocation creation time")
	}
	if observation.Sample != nil {
		if observation.Sample.ObservedAt.IsZero() || observation.Sample.ObservedAt.Unix() < 0 || observation.Sample.ObservedAt.After(observation.ResolvedAt) {
			return v1.RuntimeObservation{}, errors.New("invalid Runtime sample time")
		}
		if observation.Sample.StartedAt != nil &&
			(observation.Sample.StartedAt.IsZero() || observation.Sample.StartedAt.Unix() < 0 || observation.Sample.StartedAt.After(observation.Sample.ObservedAt)) {
			return v1.RuntimeObservation{}, errors.New("invalid Runtime start time")
		}
	}
	result := v1.RuntimeObservation{
		ID: observation.Target.SessionID, Object: "agent.runtime_observation", SessionID: observation.Target.SessionID,
		Mode: string(observation.Target.Mode), Status: string(observation.Status), ResolvedAt: observation.ResolvedAt.Unix(),
	}
	if observation.Target.EnvironmentID != "" {
		result.EnvironmentID = &observation.Target.EnvironmentID
	}
	if observation.ProviderType != "" {
		if !runtimeProviderTypePattern.MatchString(observation.ProviderType) {
			return v1.RuntimeObservation{}, errors.New("invalid Runtime observation provider type")
		}
		result.ProviderType = &observation.ProviderType
	}
	if observation.Reason != "" {
		result.Reason = &observation.Reason
	}
	switch observation.Target.Mode {
	case runtimeobs.ModeManaged:
		result.Instance.Kind = "managed_allocation"
		if observation.Target.Instance.AllocationID != "" {
			result.Instance.AllocationID = &observation.Target.Instance.AllocationID
		}
		if observation.Target.Instance.DeviceID != "" {
			result.Instance.DeviceID = &observation.Target.Instance.DeviceID
		}
		if !observation.Target.Instance.AllocationCreatedAt.IsZero() {
			created := observation.Target.Instance.AllocationCreatedAt.Unix()
			result.AllocationCreatedAt = &created
		}
	case runtimeobs.ModeSelfHosted:
		result.Instance.Kind = "self_hosted_connection"
		if observation.Target.Instance.DeviceID != "" {
			result.Instance.DeviceID = &observation.Target.Instance.DeviceID
		}
		if observation.Target.Instance.ConnectionGeneration != "" {
			result.Instance.ConnectionGeneration = &observation.Target.Instance.ConnectionGeneration
		}
	case runtimeobs.ModeNone:
		result.Instance.Kind = "none"
	default:
		return v1.RuntimeObservation{}, errors.New("invalid Runtime observation mode")
	}
	if observation.Sample == nil {
		return result, nil
	}
	observedAt := observation.Sample.ObservedAt.Unix()
	result.ObservedAt = &observedAt
	if observation.Sample.StartedAt != nil {
		startedAt := observation.Sample.StartedAt.Unix()
		result.StartedAt = &startedAt
	}
	if observation.Sample.CPUUsageSecondsTotal != nil || observation.Sample.CPUCapacityCores != nil {
		result.CPU = &v1.RuntimeCPUObservation{UsageSecondsTotal: observation.Sample.CPUUsageSecondsTotal, CapacityCores: observation.Sample.CPUCapacityCores}
	}
	if observation.Sample.MemoryUsageBytes != nil || observation.Sample.MemoryLimitBytes != nil {
		result.Memory = &v1.RuntimeMemoryObservation{UsageBytes: observation.Sample.MemoryUsageBytes, LimitBytes: observation.Sample.MemoryLimitBytes}
	}
	return result, nil
}
