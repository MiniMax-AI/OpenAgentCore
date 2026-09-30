package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
)

type metricsFixture struct {
	calls, refusals int
	name            string
	err             error
}

func (f *metricsFixture) Read(_ context.Context, name string) (coremetrics.View, error) {
	f.calls++
	f.name = name
	return coremetrics.View{Object: "core.metrics"}, f.err
}
func (f *metricsFixture) RecordUnavailable() { f.refusals++ }
func TestCoreMetricsAdministratorContract(t *testing.T) {
	key := callerBinding()
	auth, _ := NewAuthenticator([]APIKey{key})
	admin, _ := NewDeploymentAuthenticator([]string{runtimedevice.HashCredential("admin")})
	f := &metricsFixture{}
	h, err := NewHandler(&recordingStore{}, auth, "codex", WithProjectAPIKeys(managementProjectStore(key), admin), WithCoreMetrics(f))
	if err != nil {
		t.Fatal(err)
	}
	path := "/core/v1/metrics"
	for _, token := range []string{"", "unknown", "caller"} {
		w := projectKeyHTTP(h, "GET", path, token, "")
		if w.Code != 401 || f.calls != 0 {
			t.Fatal(w.Code, f.calls)
		}
	}
	for _, name := range []string{"1h", "6h", "24h", "7d"} {
		w := projectKeyHTTP(h, "GET", path+"?range="+name, "admin", "")
		if w.Code != 200 || f.name != name {
			t.Fatal(w.Code, f.name)
		}
	}
	w := projectKeyHTTP(h, "GET", path, "admin", "")
	if w.Code != 200 || f.name != "1h" {
		t.Fatal(w.Code, f.name)
	}
	before := f.calls
	for _, query := range []string{"?range=", "?range=1h&range=6h", "?project_id=x", "?range=1h&tenant_id=x", "?range=1h;secret=x", "?range=%xx", "?range=8d"} {
		w := projectKeyHTTP(h, "GET", path+query, "admin", "")
		if w.Code != 400 || f.calls != before {
			t.Fatal(query, w.Code, f.calls)
		}
	}
	if w := projectKeyHTTP(h, "POST", path, "admin", "{}"); w.Code != 405 {
		t.Fatal(w.Code)
	}
	f.err = errors.New("private credentials")
	if w := projectKeyHTTP(h, "GET", path, "admin", ""); w.Code != 503 || strings.Contains(w.Body.String(), f.err.Error()) {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestCoreRejectionObservationPreservesResponsesAndFlush(t *testing.T) {
	for _, code := range []string{"execution_unavailable", "environment_unavailable", "invalid_api_key"} {
		f := &metricsFixture{}
		h := &Handler{coreMetrics: f}
		out := httptest.NewRecorder()
		handler := h.responseHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, 503, code, "not recorded")
			writeError(w, 503, code, "second write")
		}))
		handler.ServeHTTP(out, httptest.NewRequest("GET", "/v1/agents/sessions", nil))
		want := 0
		if code == "execution_unavailable" {
			want = 1
		}
		if f.refusals != want {
			t.Fatal(code, f.refusals)
		}
	}
	f := &metricsFixture{}
	h := &Handler{coreMetrics: f}
	out := httptest.NewRecorder()
	h.responseHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
		writeError(w, 503, "execution_unavailable", "already streaming")
	})).ServeHTTP(out, httptest.NewRequest("GET", "/", nil))
	if !out.Flushed || f.refusals != 0 {
		t.Fatal("stream response changed or counted as refusal")
	}
}
