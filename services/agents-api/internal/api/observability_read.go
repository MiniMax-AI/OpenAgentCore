package api

import (
	"context"
	"net/http"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

type OperatorMetricsStore interface {
	ReadOperatorMetrics(context.Context, time.Time, time.Time, time.Duration) (store.OperatorMetrics, error)
}

type OperatorMetricsResponse struct {
	GeneratedAt time.Time                     `json:"generated_at"`
	Start       time.Time                     `json:"start"`
	End         time.Time                     `json:"end"`
	StepSeconds int64                         `json:"step_seconds"`
	Requests    []store.RequestMetricBucket   `json:"requests"`
	Collector   []store.CollectorMetricBucket `json:"collector"`
	Turns       []store.TurnMetricBucket      `json:"turns"`
	Tools       []store.ToolMetricBucket      `json:"tools"`
}

func WithOperatorMetrics(s OperatorMetricsStore, auth *DeploymentAuthenticator) Option {
	return func(h *Handler) {
		h.operatorMetrics = s
		if auth != nil {
			h.deploymentAuth = auth
		}
	}
}

func (h *Handler) registerOperatorMetricsRoutes(r chi.Router) {
	if h.operatorMetrics == nil || h.deploymentAuth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(h.deploymentAuth.authenticate)
		r.Get("/core/v1/observability/summary", h.getOperatorMetrics)
		r.Head("/core/v1/observability/summary", methodNotAllowed)
	})
}

// @Summary Retrieve bounded operator observability metrics
// @Description Deployment administrator only. Returns fixed-range aggregate requests and collector coverage without paths, payloads or project identifiers.
// @Tags Observability
// @Produce json
// @Security DeploymentAdminAuth
// @Param range query string false "1h, 6h, or 24h (default 1h)"
// @Success 200 {object} api.OperatorMetricsResponse
// @Failure 400,401,500,503 {object} v1.ErrorResponse
// @Router /core/v1/observability/summary [get]
func (h *Handler) getOperatorMetrics(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	for key, entries := range values {
		if key != "range" || len(entries) != 1 {
			writeStoreError(w, r, store.ErrInvalidInput)
			return
		}
	}
	span, step := time.Hour, time.Minute
	switch values.Get("range") {
	case "", "1h":
	case "6h":
		span, step = 6*time.Hour, 5*time.Minute
	case "24h":
		span, step = 24*time.Hour, 15*time.Minute
	default:
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	end := time.Now().UTC().Truncate(step)
	start := end.Add(-span)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	metrics, err := h.operatorMetrics.ReadOperatorMetrics(ctx, start, end, step)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, OperatorMetricsResponse{
		GeneratedAt: time.Now().UTC(), Start: start, End: end, StepSeconds: int64(step / time.Second),
		Requests: metrics.Requests, Collector: metrics.Collector, Turns: metrics.Turns, Tools: metrics.Tools,
	})
}
