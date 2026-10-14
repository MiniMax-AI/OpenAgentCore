package api

import (
	"context"
	"net/http"
	"net/url"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
)

// Metrics reads Core's process and job metrics and counts rejected execution.
type Metrics interface {
	Read(context.Context, string) (coremetrics.View, error)
	RecordUnavailable()
}

// @Summary Retrieve Core operational metrics
// @Description Core key only. Complete UTC buckets; unknown measurements are null. Samples are process-local and are not backfilled after a restart.
// @Tags Core Administration
// @Produce json
// @Security DeploymentAdminAuth
// @Param range query string false "Time range (default 1h)" Enums(1h,6h,24h,7d)
// @Success 200 {object} coremetrics.View
// @Failure 400,401,503 {object} CoreErrorResponse
// @Router /core/v1/metrics [get]
func (h *Handler) getCoreMetrics(w http.ResponseWriter, r *http.Request) {
	name, ok := rangeName(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "Only one supported range parameter is allowed.")
		return
	}
	switch name {
	case "1h", "6h", "24h", "7d":
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "range must be 1h, 6h, 24h or 7d.")
		return
	}
	value, err := h.Metrics.Read(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "core_metrics_unavailable", "Core metrics could not be read.")
		return
	}
	writeJSON(w, http.StatusOK, value)
}

// rangeName reads the one query parameter a metrics read accepts: a range,
// 1h when absent. Core's clock ends the range, so a caller needs no clock.
func rangeName(r *http.Request) (string, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) > 1 || len(values) == 1 && len(values["range"]) != 1 {
		return "", false
	}
	if v, ok := values["range"]; ok {
		return v[0], true
	}
	return "1h", true
}

func (h *Handler) responseHeaders(next http.Handler) http.Handler {
	return responseHeadersWithLog(next, func(code string) {
		if code == "execution_unavailable" {
			h.Metrics.RecordUnavailable()
		}
	}, h.requestLogs)
}
