package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory"
	"github.com/go-chi/chi/v5"
)

const (
	defaultRuntimeHistoryPoints = 120
	runtimeHistoryRequestBudget = 15 * time.Second
)

type RuntimeHistoryService interface {
	Capabilities() runtimehistory.Capabilities
	QuerySession(context.Context, string, string, runtimehistory.Range) (runtimehistory.Response, error)
}

func WithRuntimeHistory(service RuntimeHistoryService) Option {
	return func(h *Handler) { h.runtimeHistory = service }
}

// @Summary Retrieve Runtime history capabilities
// @Description Core extension advertising only safe backend-neutral Durable history availability and bounds. Available is true only when a query Reader and qualified periodic collection are both configured.
// @Tags Runtime history
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Success 200 {object} v1.RuntimeHistoryCapabilities
// @Failure 400,401 {object} v1.ErrorResponse
// @Router /agents/runtime-history/capabilities [get]
func (h *Handler) getRuntimeHistoryCapabilities(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Runtime history capabilities do not accept query parameters.")
		return
	}
	writeJSON(w, http.StatusOK, runtimeHistoryCapabilitiesResponse(h.runtimeHistory))
}

// @Summary Retrieve Session Runtime history
// @Description Core extension returning tenant-scoped stored Runtime observations for one Session. End is exclusive; the server selects a bounded resolution. Responses contain at most 1,000 series, 10,000 points per coverage/series array, and 100,000 total coverage plus series points. It never reads or changes live compute.
// @Tags Runtime history
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param start query integer true "Inclusive Unix-second start" minimum(0) maximum(9007199254740991)
// @Param end query integer true "Exclusive Unix-second end" minimum(1) maximum(9007199254740991)
// @Param max_points query integer false "Maximum points per series; defaults to the lower of 120 and the advertised service maximum" minimum(2) maximum(10000)
// @Success 200 {object} v1.RuntimeHistory
// @Failure 400,401,404,409,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/runtime-history [get]
func (h *Handler) getRuntimeHistory(w http.ResponseWriter, r *http.Request) {
	if h.runtimeHistory == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime_history_unavailable", "Durable Runtime history is not configured on this service.")
		return
	}
	capabilities := h.runtimeHistory.Capabilities()
	if capabilities.Validate() != nil || !capabilities.Durable() {
		writeError(w, http.StatusServiceUnavailable, "runtime_history_unavailable", "Durable Runtime history is not configured on this service.")
		return
	}
	requested, ok := readRuntimeHistoryRange(w, r, capabilities, time.Now().UTC())
	if !ok {
		return
	}
	expectedTenantID := tenantID(r)
	expectedSessionID := chi.URLParam(r, "session_id")
	ctx, cancel := context.WithTimeout(r.Context(), runtimeHistoryRequestBudget)
	defer cancel()
	value, err := h.runtimeHistory.QuerySession(ctx, expectedTenantID, expectedSessionID, requested)
	if err != nil {
		switch {
		case errors.Is(err, runtimehistory.ErrInvalidRange):
			writeError(w, http.StatusBadRequest, "invalid_request", "Runtime history range is invalid.")
		case errors.Is(err, runtimehistory.ErrUnsupported):
			writeError(w, http.StatusConflict, "runtime_history_unsupported", "Runtime history is not supported for this Session.")
		case errors.Is(err, runtimehistory.ErrUnavailable), errors.Is(err, runtimehistory.ErrInvalidResult), errors.Is(err, context.DeadlineExceeded):
			log.Ctx(r.Context()).Warn("Runtime history query unavailable")
			writeError(w, http.StatusServiceUnavailable, "runtime_history_unavailable", "Durable Runtime history is temporarily unavailable.")
		default:
			writeStoreError(w, r, err)
		}
		return
	}
	response, err := runtimeHistoryResponse(value, expectedTenantID, expectedSessionID, requested)
	if err != nil {
		log.Ctx(r.Context()).Error("Runtime history response validation failed")
		writeError(w, http.StatusServiceUnavailable, "runtime_history_unavailable", "Durable Runtime history is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func readRuntimeHistoryRange(w http.ResponseWriter, r *http.Request, capabilities runtimehistory.Capabilities, now time.Time) (runtimehistory.Range, bool) {
	query := r.URL.Query()
	for key, values := range query {
		if key != "start" && key != "end" && key != "max_points" || len(values) != 1 {
			writeError(w, http.StatusBadRequest, "unsupported_parameter", "Runtime history accepts one start, end and optional max_points value.")
			return runtimehistory.Range{}, false
		}
	}
	start, startErr := strconv.ParseInt(query.Get("start"), 10, 64)
	end, endErr := strconv.ParseInt(query.Get("end"), 10, 64)
	points := defaultRuntimeHistoryPoints
	if capabilities.MaximumPoints < points {
		points = capabilities.MaximumPoints
	}
	if raw := query.Get("max_points"); raw != "" {
		var err error
		points, err = strconv.Atoi(raw)
		if err != nil || points < 2 || points > capabilities.MaximumPoints {
			writeError(w, http.StatusBadRequest, "invalid_request", "Runtime history range is invalid.")
			return runtimehistory.Range{}, false
		}
	}
	if startErr != nil || endErr != nil || start < 0 || end <= start || end > now.Unix()+1 || end-start > int64(capabilities.MaximumRange/time.Second) {
		writeError(w, http.StatusBadRequest, "invalid_request", "Runtime history range is invalid.")
		return runtimehistory.Range{}, false
	}
	return runtimehistory.Range{Start: time.Unix(start, 0).UTC(), End: time.Unix(end, 0).UTC(), MaxPoints: points}, true
}

func runtimeHistoryCapabilitiesResponse(service RuntimeHistoryService) v1.RuntimeHistoryCapabilities {
	response := v1.RuntimeHistoryCapabilities{Object: "agent.runtime_history_capabilities", Metrics: []string{}}
	if service == nil {
		reason := "not_configured"
		response.Reason = &reason
		return response
	}
	capabilities := service.Capabilities()
	if capabilities.Validate() != nil {
		reason := "not_configured"
		response.Reason = &reason
		return response
	}
	mode := string(capabilities.CollectionMode)
	retention := int64(capabilities.Retention / time.Second)
	minimumStep := int64(capabilities.MinimumStep / time.Second)
	maximumRange := int64(capabilities.MaximumRange / time.Second)
	maximumPoints := capabilities.MaximumPoints
	response.CollectionMode = &mode
	response.RetentionSeconds = &retention
	response.MinimumStepSeconds = &minimumStep
	response.MaximumRangeSeconds = &maximumRange
	response.MaximumPoints = &maximumPoints
	response.Metrics = make([]string, len(capabilities.Metrics))
	for index, metric := range capabilities.Metrics {
		response.Metrics[index] = string(metric)
	}
	if capabilities.SampleInterval > 0 {
		seconds := int64(capabilities.SampleInterval / time.Second)
		response.SampleIntervalSeconds = &seconds
	}
	response.Available = capabilities.Durable()
	if !response.Available {
		reason := "periodic_collection_required"
		response.Reason = &reason
	}
	return response
}

func runtimeHistoryResponse(value runtimehistory.Response, expectedTenantID, expectedSessionID string, expectedRange runtimehistory.Range) (v1.RuntimeHistory, error) {
	if err := value.Validate(time.Now().UTC()); err != nil || !value.Durable() ||
		value.TenantID != expectedTenantID || value.SessionID != expectedSessionID ||
		!value.Requested.Start.Equal(expectedRange.Start) || !value.Requested.End.Equal(expectedRange.End) || value.Requested.MaxPoints != expectedRange.MaxPoints {
		return v1.RuntimeHistory{}, runtimehistory.ErrInvalidResult
	}
	retainedStart := value.Requested.Start
	configuredStart := value.GeneratedAt.Add(-value.Retention)
	if configuredStart.After(retainedStart) {
		retainedStart = configuredStart
	}
	if value.RetainedFrom != nil && value.RetainedFrom.After(retainedStart) {
		retainedStart = *value.RetainedFrom
	}
	if retainedStart.After(value.Requested.End) {
		retainedStart = value.Requested.End
	}
	coverage := v1.RuntimeHistoryCoverage{RetainedStart: retainedStart.Unix(), Buckets: make([]v1.RuntimeHistoryCoveragePoint, 0, len(value.Coverage))}
	if retainedStart.Before(value.Requested.End) {
		duration := value.Requested.End.Sub(retainedStart)
		coverage.ExpectedSampleCount = int64(duration / value.SampleInterval)
		if duration%value.SampleInterval != 0 {
			coverage.ExpectedSampleCount++
		}
	}
	for _, point := range value.Coverage {
		converted := v1.RuntimeHistoryCoveragePoint{
			Start: point.Start.Unix(), End: point.End.Unix(), ObservationCount: point.ObservationCount,
			ObservedCount: point.ObservedCount, UnavailableCount: point.UnavailableCount,
		}
		if point.ObservationCount > 0 {
			first, last := point.FirstObservedAt.Unix(), point.LastObservedAt.Unix()
			converted.FirstObservedAt, converted.LastObservedAt = &first, &last
			if coverage.FirstSampleAt == nil || first < *coverage.FirstSampleAt {
				value := first
				coverage.FirstSampleAt = &value
			}
			if coverage.LastSampleAt == nil || last > *coverage.LastSampleAt {
				value := last
				coverage.LastSampleAt = &value
			}
		}
		coverage.SampleCount += int64(point.ObservationCount)
		coverage.Buckets = append(coverage.Buckets, converted)
	}
	response := v1.RuntimeHistory{
		Object: "agent.runtime_history", Source: "durable", SessionID: value.SessionID,
		RequestedRange:    v1.RuntimeHistoryRange{Start: value.Requested.Start.Unix(), End: value.Requested.End.Unix()},
		ResolutionSeconds: int64(value.Resolution / time.Second), GeneratedAt: value.GeneratedAt.Unix(), Coverage: coverage,
		Series:     make([]v1.RuntimeHistorySeries, 0, len(value.Series)),
		TokenUsage: make([]v1.RuntimeHistoryTokenUsagePoint, 0, len(value.TokenUsage)),
	}
	for _, point := range value.TokenUsage {
		response.TokenUsage = append(response.TokenUsage, v1.RuntimeHistoryTokenUsagePoint{
			Start: point.Start.Unix(), End: point.End.Unix(), SampledAt: point.SampledAt.Unix(),
			InputTokens: point.InputTokens, OutputTokens: point.OutputTokens,
		})
	}
	for _, series := range value.Series {
		converted := v1.RuntimeHistorySeries{
			EnvironmentID: series.EnvironmentID, AllocationID: series.AllocationID,
			StartedAt: v1.RuntimeHistoryTime{Seconds: series.StartedAt.Unix(), Nanoseconds: series.StartedAt.Nanosecond()}, ProviderType: series.ProviderType,
			Points: make([]v1.RuntimeHistoryPoint, 0, len(series.Points)),
		}
		for _, point := range series.Points {
			item := v1.RuntimeHistoryPoint{
				Start: point.Start.Unix(), End: point.End.Unix(), ObservationCount: point.ObservationCount,
				ObservedCount: point.ObservedCount, UnavailableCount: point.UnavailableCount,
			}
			if point.ObservationCount > 0 {
				first, last := point.FirstObservedAt.Unix(), point.LastObservedAt.Unix()
				item.FirstObservedAt, item.LastObservedAt = &first, &last
			}
			if point.CPUContributorCount > 0 {
				item.CPU = &v1.RuntimeHistoryCPU{ContributorCount: point.CPUContributorCount, UtilizationRatio: point.CPUUtilizationRatio, CapacityCores: point.CPUCapacityCores}
			}
			if point.MemoryContributorCount > 0 {
				item.Memory = &v1.RuntimeHistoryMemory{ContributorCount: point.MemoryContributorCount, UsageBytes: point.MemoryUsageBytes, LimitBytes: point.MemoryLimitBytes}
			}
			converted.Points = append(converted.Points, item)
		}
		response.Series = append(response.Series, converted)
	}
	return response, nil
}
