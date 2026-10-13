package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/go-chi/chi/v5"
)

const requestLogCapacity = 1000

// RequestLogEntry contains HTTP transport metadata, never request content.
type RequestLogEntry struct {
	RequestID     string    `json:"request_id" binding:"required"`
	TraceID       string    `json:"trace_id" binding:"required"`
	StartedAt     time.Time `json:"started_at" binding:"required"`
	Method        string    `json:"method" binding:"required"`
	Route         string    `json:"route" binding:"required"`
	State         string    `json:"state" binding:"required" enums:"active,completed,aborted"`
	Status        *int      `json:"status" binding:"required" extensions:"x-nullable"`
	DurationMS    float64   `json:"duration_ms" binding:"required"`
	ResponseBytes int64     `json:"response_bytes" binding:"required"`
	ErrorCode     *string   `json:"error_code" binding:"required" extensions:"x-nullable"`
}

type RequestLogList struct {
	Object            string            `json:"object" binding:"required"`
	Surface           string            `json:"surface" binding:"required" enums:"public,admin"`
	InstanceID        string            `json:"instance_id" binding:"required"`
	InstanceStartedAt time.Time         `json:"instance_started_at" binding:"required"`
	Capacity          int               `json:"capacity" binding:"required"`
	RetainedCount     int               `json:"retained_count" binding:"required"`
	TotalMatching     int               `json:"total_matching" binding:"required"`
	HasMore           bool              `json:"has_more" binding:"required"`
	Data              []RequestLogEntry `json:"data" binding:"required"`
}

// Slots are copied while locked. A late completion can only update its own
// sequence, so eviction of a long-running stream cannot resurrect old entries.
type requestLogs struct {
	mu         sync.Mutex
	slots      [2][requestLogCapacity]RequestLogEntry
	sequence   [2]uint64
	instanceID string
	started    time.Time
	routes     chi.Routes
}

func newRequestLogs(routes chi.Routes) *requestLogs {
	return &requestLogs{instanceID: newRequestID(), started: time.Now().UTC(), routes: routes}
}

func requestLogMethod(method string) string {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
		return method
	}
	return "OTHER"
}

func (l *requestLogs) begin(r *http.Request, id string, started time.Time, surface int) (uint64, RequestLogEntry) {
	routePath := r.URL.RawPath
	if routePath == "" {
		routePath = r.URL.Path
	}
	route := "(unmatched)"
	// Find returns authored patterns only; no URL values or queries are retained.
	methods := []string{r.Method, "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
	for _, method := range methods {
		if pattern := l.routes.Find(chi.NewRouteContext(), method, routePath); pattern != "" {
			route = pattern
			break
		}
	}
	carrier, _ := log.TraceFromContext(r.Context())
	entry := RequestLogEntry{RequestID: id, TraceID: carrier.Trace.String(), StartedAt: started.UTC(), Method: requestLogMethod(r.Method), Route: route, State: "active"}
	l.mu.Lock()
	seq := l.sequence[surface]
	l.sequence[surface]++
	l.slots[surface][seq%requestLogCapacity] = entry
	l.mu.Unlock()
	return seq, entry
}

func (l *requestLogs) update(seq uint64, entry RequestLogEntry, surface int) {
	l.mu.Lock()
	if l.sequence[surface]-seq <= requestLogCapacity {
		l.slots[surface][seq%requestLogCapacity] = entry
	}
	l.mu.Unlock()
}

func (l *requestLogs) snapshot(limit int, id, outcome, method string, surface int) RequestLogList {
	l.mu.Lock()
	slots, sequence := l.slots[surface], l.sequence[surface]
	l.mu.Unlock()
	count := min(sequence, uint64(requestLogCapacity))
	result := RequestLogList{Object: "core.request_log.list", InstanceID: l.instanceID, InstanceStartedAt: l.started, Capacity: requestLogCapacity, RetainedCount: int(count), Data: make([]RequestLogEntry, 0, limit)}
	result.Surface = []string{"public", "admin"}[surface]
	now := time.Now()
	for offset := uint64(0); offset < count; offset++ {
		entry := slots[(sequence-1-offset)%requestLogCapacity]
		if id != "" && entry.RequestID != id || method != "" && entry.Method != method || outcome == "active" && entry.State != "active" || outcome == "errors" && entry.State != "aborted" && (entry.Status == nil || *entry.Status < 400) {
			continue
		}
		result.TotalMatching++
		if len(result.Data) >= limit {
			continue
		}
		if entry.State == "active" {
			entry.DurationMS = float64(now.Sub(entry.StartedAt)) / float64(time.Millisecond)
		}
		result.Data = append(result.Data, entry)
	}
	result.HasMore = result.TotalMatching > len(result.Data)
	return result
}

// @Summary List recent Core HTTP requests
// @Description Core key only. Process-local bounded metadata for /v1 and /core/v1, newest started first. Polling this endpoint is excluded. HTTP status is not a Turn outcome.
// @Tags Core Administration
// @Produce json
// @Security DeploymentAdminAuth
// @Param surface query string false "API surface (default public)" Enums(public,admin)
// @Param limit query int false "Maximum entries (default 100)" minimum(1) maximum(200)
// @Param request_id query string false "Exact server-issued request ID"
// @Param outcome query string false "HTTP outcome filter (default all)" Enums(all,errors,active)
// @Param method query string false "HTTP method" Enums(GET,HEAD,POST,PUT,PATCH,DELETE,OPTIONS,CONNECT,TRACE,OTHER)
// @Success 200 {object} RequestLogList
// @Failure 400,401 {object} CoreErrorResponse
// @Router /core/v1/request-logs [get]
func (h *Handler) getRequestLogs(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	limit := 100
	valid := err == nil
	for key, value := range values {
		if len(value) != 1 {
			valid = false
		}
		switch key {
		case "limit", "request_id", "outcome", "method", "surface":
		default:
			valid = false
		}
	}
	if value, ok := values["limit"]; ok && len(value) == 1 {
		limit, err = strconv.Atoi(value[0])
		valid = valid && err == nil && limit >= 1 && limit <= 200
	}
	id, outcome, method := values.Get("request_id"), values.Get("outcome"), values.Get("method")
	if _, ok := values["request_id"]; ok {
		valid = valid && len(id) == 36 && strings.HasPrefix(id, "req_")
		for _, c := range strings.TrimPrefix(id, "req_") {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				valid = false
			}
		}
	}
	if _, ok := values["outcome"]; ok {
		valid = valid && (outcome == "all" || outcome == "errors" || outcome == "active")
	}
	if _, ok := values["method"]; ok {
		valid = valid && method != "" && (requestLogMethod(method) == method)
	}
	surface := 0
	if v, ok := values["surface"]; ok {
		valid = valid && len(v) == 1 && (v[0] == "public" || v[0] == "admin")
		if len(v) == 1 && v[0] == "admin" {
			surface = 1
		}
	}
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request log filters are invalid.")
		return
	}
	writeJSON(w, http.StatusOK, h.requestLogs.snapshot(limit, id, outcome, method, surface))
}

func recordRequest(r *http.Request) bool {
	path := r.URL.Path
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && path == "/core/v1/request-logs" {
		return false
	}
	return path == "/v1" || strings.HasPrefix(path, "/v1/") || path == "/core/v1" || strings.HasPrefix(path, "/core/v1/")
}
