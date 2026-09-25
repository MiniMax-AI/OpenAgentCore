package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/observability"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func WithRequestMetrics(recorder *observability.RequestRecorder) Option {
	return func(h *Handler) { h.requestMetrics = recorder }
}

func (h *Handler) recordRequestMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/core/v1/admin/observability" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(wrapped, r)
		status := wrapped.Status()
		if status == 0 {
			status = http.StatusOK
		}
		outcome := "success"
		if status >= 500 {
			outcome = "server_error"
		} else if status >= 400 {
			outcome = "client_error"
		}
		method := r.Method
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead:
		default:
			method = "OTHER"
		}
		pattern := ""
		if route := chi.RouteContext(r.Context()); route != nil {
			pattern = route.RoutePattern()
		}
		h.requestMetrics.Record(observability.Request{
			CompletedAt: time.Now().UTC(), RouteFamily: requestRouteFamily(pattern),
			Method: method, Outcome: outcome, Latency: time.Since(start),
		})
	})
}

func requestRouteFamily(pattern string) string {
	switch {
	case strings.HasPrefix(pattern, "/core/v1/sandbox"):
		return "sandbox_admin"
	case strings.HasPrefix(pattern, "/core/v1"):
		return "core_admin"
	case strings.HasPrefix(pattern, "/v1/agents/sessions"):
		return "sessions"
	case strings.HasPrefix(pattern, "/v1/agents/runtime"):
		return "runtime"
	case strings.HasPrefix(pattern, "/v1/agents/environments"):
		return "environments"
	case strings.HasPrefix(pattern, "/v1/agents"):
		return "agents"
	case strings.HasPrefix(pattern, "/v1/files"):
		return "files"
	case strings.HasPrefix(pattern, "/v1/vaults"):
		return "vaults"
	case strings.HasPrefix(pattern, "/v1/skills"):
		return "skills"
	default:
		return "other"
	}
}
