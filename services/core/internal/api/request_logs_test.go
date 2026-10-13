package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func TestRequestLogsAdministratorContract(t *testing.T) {
	deps, _ := managementFakes(t, callerBinding())
	handler := newTestHandler(t, deps)
	for _, key := range []string{"", "unknown", "caller"} {
		if w := projectKeyHTTP(handler, "GET", "/core/v1/request-logs", key, ""); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	secret := "secret-query-body-key"
	response := projectKeyHTTP(handler, "POST", "/v1/files/"+secret+"?secret="+secret, secret, secret)
	w := projectKeyHTTP(handler, "GET", "/core/v1/request-logs", "admin", "")
	var list RequestLogList
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || list.Surface != "public" || len(list.Data) != 1 || list.Data[0].Status == nil || *list.Data[0].Status != response.Code || list.Data[0].RequestID != response.Header().Get("X-Request-Id") {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), secret) || list.Data[0].Route != "/v1/files/{file_id}" || len(list.Data[0].TraceID) != 32 || list.InstanceStartedAt.IsZero() || list.InstanceID == "" {
		t.Fatal(w.Body.String())
	}
	for _, query := range []string{"limit=0", "limit=201", "limit=x", "limit=1&limit=2", "outcome=", "outcome=done", "method=evil", "method=", "surface=bad", "request_id=secret", "request_id=", "extra=secret", "limit=1;secret=x", "limit=%xx"} {
		if w := projectKeyHTTP(handler, "GET", "/core/v1/request-logs?"+query, "admin", ""); w.Code != 400 || strings.Contains(w.Body.String(), secret) {
			t.Fatal(query, w.Code, w.Body.String())
		}
	}
	w = projectKeyHTTP(handler, "GET", "/core/v1/request-logs?request_id="+list.Data[0].RequestID+"&outcome=errors&method=POST&limit=1", "admin", "")
	var filtered RequestLogList
	if err := json.Unmarshal(w.Body.Bytes(), &filtered); err != nil || len(filtered.Data) != 1 {
		t.Fatal(err, w.Body.String())
	}
	w = projectKeyHTTP(handler, "GET", "/core/v1/request-logs?surface=admin", "admin", "")
	if err := json.Unmarshal(w.Body.Bytes(), &filtered); err != nil || len(filtered.Data) != 0 {
		t.Fatal(err, w.Body.String())
	}
}

func TestRequestLogsIncludeAuthenticationFailures(t *testing.T) {
	deps, _ := managementFakes(t, callerBinding())
	handler := newTestHandler(t, deps)
	for _, surface := range []struct{ name, path string }{{"public", "/v1/files"}, {"admin", "/core/v1/summary"}} {
		response := projectKeyHTTP(handler, "GET", surface.path, "invalid-secret-token", "")
		if response.Code != http.StatusUnauthorized {
			t.Fatal(surface.name, response.Code)
		}
		query := "/core/v1/request-logs?surface=" + surface.name + "&outcome=errors&request_id=" + response.Header().Get("X-Request-Id")
		result := projectKeyHTTP(handler, "GET", query, "admin", "")
		var list RequestLogList
		if err := json.Unmarshal(result.Body.Bytes(), &list); err != nil || len(list.Data) != 1 || list.Data[0].Status == nil || *list.Data[0].Status != http.StatusUnauthorized || list.Data[0].Route != surface.path {
			t.Fatal(surface.name, err, result.Body.String())
		}
	}
}

func requestLogRouter() (*chi.Mux, *requestLogs) {
	router := chi.NewRouter()
	logs := newRequestLogs(router)
	router.Use(log.HTTPMiddleware, func(next http.Handler) http.Handler { return responseHeadersWithLog(next, nil, logs) }, middleware.GetHead)
	router.MethodNotAllowed(methodNotAllowed)
	return router, logs
}

func TestRequestLogsRoutingAndScope(t *testing.T) {
	router, logs := requestLogRouter()
	router.Get("/v1/items/{id}", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"ok": "yes"}) })
	router.Get("/core/v1/test", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, 503, "execution_unavailable", "secret response")
	})
	for _, tt := range []struct {
		method, path, route string
		status              int
	}{
		{"HEAD", "/v1/items/secret", "/v1/items/{id}", 200},
		{"POST", "/v1/items/secret", "/v1/items/{id}", 405},
		{"UNTRUSTED_SECRET_METHOD", "/v1/items/secret", "/v1/items/{id}", 405},
		{"GET", "/v1/secret-unknown", "(unmatched)", 404},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, nil))
		entry := logs.snapshot(1, "", "", "", 0).Data[0]
		if entry.Route != tt.route || entry.Method != requestLogMethod(tt.method) || entry.State != "completed" || entry.Status == nil || *entry.Status != tt.status {
			t.Fatalf("%+v", entry)
		}
	}
	for _, path := range []string{"/healthz", "/docs", "/api/v1/connection", "/core/v1/request-logs"} {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	if got := logs.snapshot(100, "", "", "", 0).RetainedCount; got != 4 {
		t.Fatal(got)
	}
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/core/v1/test", nil))
	admin := logs.snapshot(1, "", "errors", "", 1)
	if len(admin.Data) != 1 || admin.Data[0].ErrorCode == nil || *admin.Data[0].ErrorCode != "execution_unavailable" {
		t.Fatal(admin)
	}
}

func TestRequestLogsObserveActiveStreamingAndPanic(t *testing.T) {
	router, logs := requestLogRouter()
	entered, flush, flushed, release := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	router.Get("/v1/stream", func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-flush
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			panic(err)
		}
		close(flushed)
		<-release
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/stream", nil))
	}()
	<-entered
	first := logs.snapshot(1, "", "active", "", 0).Data[0]
	if first.Status != nil || first.State != "active" || first.ResponseBytes != 0 {
		t.Fatal(first)
	}
	close(flush)
	<-flushed
	streaming := logs.snapshot(1, "", "active", "", 0).Data[0]
	if streaming.Status == nil || *streaming.Status != 200 || streaming.ResponseBytes != 13 || streaming.DurationMS < first.DurationMS {
		t.Fatal(streaming)
	}
	close(release)
	<-done
	if entry := logs.snapshot(1, "", "", "", 0).Data[0]; entry.State != "completed" {
		t.Fatal(entry)
	}
	router.Get("/v1/panic", func(http.ResponseWriter, *http.Request) { panic("private panic") })
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic swallowed")
			}
		}()
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/panic", nil))
	}()
	entry := logs.snapshot(1, "", "errors", "", 0).Data[0]
	if entry.State != "aborted" || entry.Status != nil || entry.ErrorCode != nil {
		t.Fatal(entry)
	}
}

type requestLogFailWriter struct{ header http.Header }

func (w requestLogFailWriter) Header() http.Header     { return w.header }
func (requestLogFailWriter) WriteHeader(int)           {}
func (requestLogFailWriter) Write([]byte) (int, error) { return 2, errors.New("private failure") }
func TestRequestLogsWriteFailure(t *testing.T) {
	router, logs := requestLogRouter()
	router.Get("/v1/failure", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("body")) })
	router.ServeHTTP(requestLogFailWriter{make(http.Header)}, httptest.NewRequest("GET", "/v1/failure", nil))
	entry := logs.snapshot(1, "", "errors", "", 0).Data[0]
	if entry.State != "aborted" || entry.ResponseBytes != 2 || entry.Status == nil || *entry.Status != 200 {
		t.Fatal(entry)
	}
}

func TestRequestLogsBoundsEvictionAndConcurrentSnapshots(t *testing.T) {
	router := chi.NewRouter()
	router.Get("/v1/test", func(http.ResponseWriter, *http.Request) {})
	logs := newRequestLogs(router)
	request := httptest.NewRequest("GET", "/v1/test", nil)
	seq, old := logs.begin(request, "old-active", time.Now(), 0)
	for i := 0; i < requestLogCapacity; i++ {
		logs.begin(request, "new", time.Now(), 0)
	}
	old.State = "completed"
	logs.update(seq, old, 0)
	if list := logs.snapshot(200, "", "", "", 0); list.RetainedCount != 1000 || list.TotalMatching != 1000 || !list.HasMore || len(list.Data) != 200 {
		t.Fatal(list)
	}
	if list := logs.snapshot(1, "old-active", "", "", 0); list.TotalMatching != 0 {
		t.Fatal("evicted entry resurrected")
	}
	for i := 0; i < requestLogCapacity+1; i++ {
		logs.begin(request, "admin", time.Now(), 1)
	}
	if list := logs.snapshot(1, "", "", "", 0); list.Data[0].RequestID != "new" {
		t.Fatal("admin evicted public")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				seq, entry := logs.begin(request, "concurrent", time.Now(), 0)
				entry.State = "completed"
				logs.update(seq, entry, 0)
				logs.snapshot(20, "", "", "", 0)
			}
		}()
	}
	wg.Wait()
}

func BenchmarkRequestLogging(b *testing.B) {
	previous := slog.Default()
	slog.SetDefault(slog.New(log.NewContextHandler(slog.NewJSONHandler(io.Discard, nil))))
	defer slog.SetDefault(previous)
	for _, enabled := range []bool{false, true} {
		name := "baseline"
		if enabled {
			name = "recorded"
		}
		b.Run(name, func(b *testing.B) {
			router := chi.NewRouter()
			var logs *requestLogs
			if enabled {
				logs = newRequestLogs(router)
			}
			router.Use(log.HTTPMiddleware, func(next http.Handler) http.Handler { return responseHeadersWithLog(next, nil, logs) })
			router.Get("/v1/items/{id}", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
			request := httptest.NewRequest("GET", "/v1/items/test", nil)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				router.ServeHTTP(httptest.NewRecorder(), request)
			}
		})
	}
	b.Run("snapshot-full", func(b *testing.B) {
		router := chi.NewRouter()
		logs := newRequestLogs(router)
		request := httptest.NewRequest("GET", "/v1/missing", nil)
		for i := 0; i < requestLogCapacity; i++ {
			logs.begin(request, "test", time.Now(), 0)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			logs.snapshot(100, "", "", "", 0)
		}
	})
}

func TestRequestLogsPreserveImplicitResponseAndCancellation(t *testing.T) {
	router, logs := requestLogRouter()
	router.Get("/v1/implicit", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "<html>hello</html>") })
	server := httptest.NewServer(router)
	defer server.Close()
	for _, method := range []string{"GET", "HEAD"} {
		request, _ := http.NewRequest(method, server.URL+"/v1/implicit", nil)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/html; charset=utf-8" || response.ContentLength != 18 {
			t.Fatal(method, response.StatusCode, response.Header, err)
		}
		if method == "HEAD" && len(body) != 0 || method == "GET" && string(body) != "<html>hello</html>" {
			t.Fatal(method, string(body))
		}
	}
	router.Get("/v1/canceled", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/canceled", nil).WithContext(ctx))
	entry := logs.snapshot(1, "", "errors", "", 0).Data[0]
	if entry.State != "aborted" || entry.Status != nil {
		t.Fatal(entry)
	}
}

func TestRequestLogsStructuredCompletion(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(log.NewContextHandler(slog.NewJSONHandler(&output, nil))))
	defer slog.SetDefault(previous)
	router, _ := requestLogRouter()
	router.Get("/v1/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, 400, "invalid_request", "private response")
	})
	request := httptest.NewRequest("GET", "/v1/items/private-path?secret=private-query", strings.NewReader("private body"))
	request.Header.Set("Authorization", "Bearer private-token")
	request.Header.Set("X-Request-Id", "forged-request-id")
	writer := httptest.NewRecorder()
	router.ServeHTTP(writer, request)
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "private") || strings.Contains(output.String(), "forged") || record["request_id"] != writer.Header().Get("X-Request-Id") || record["status"] != float64(400) || record["error_code"] != "invalid_request" || len(record["trace_id"].(string)) != 32 {
		t.Fatal(output.String())
	}
}
