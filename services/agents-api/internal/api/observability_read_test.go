package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type operatorMetricsStub struct{ calls int }

func (s *operatorMetricsStub) ReadOperatorMetrics(_ context.Context, start, end time.Time, step time.Duration) (store.OperatorMetrics, error) {
	s.calls++
	if end.Sub(start) != time.Hour || step != time.Minute {
		return store.OperatorMetrics{}, store.ErrInvalidInput
	}
	return store.OperatorMetrics{Requests: []store.RequestMetricBucket{}, Collector: []store.CollectorMetricBucket{}, Turns: []store.TurnMetricBucket{}, Tools: []store.ToolMetricBucket{}}, nil
}

func TestOperatorMetricsRequireAdministratorAndBoundedRange(t *testing.T) {
	project, err := NewAuthenticator([]APIKey{callerBinding()})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := NewDeploymentAuthenticator([]string{device.HashCredential("administrator")})
	if err != nil {
		t.Fatal(err)
	}
	metrics := &operatorMetricsStub{}
	h, err := NewHandler(&recordingStore{}, project, "codex", WithOperatorMetrics(metrics, admin))
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		return response
	}
	for _, key := range []string{"caller", ""} {
		response := request("/core/v1/admin/observability?range=1h", key)
		if response.Code != http.StatusUnauthorized || metrics.calls != 0 {
			t.Fatal("project or missing key reached operator metrics", response.Code, metrics.calls)
		}
	}
	for _, path := range []string{"/core/v1/admin/observability?range=30d", "/core/v1/admin/observability?range=1h&range=24h", "/core/v1/admin/observability?tenant_id=x"} {
		response := request(path, "administrator")
		if response.Code != http.StatusBadRequest || metrics.calls != 0 {
			t.Fatal("unbounded metrics query admitted", response.Code, metrics.calls)
		}
	}
	response := request("/core/v1/admin/observability?range=1h", "administrator")
	if response.Code != http.StatusOK || metrics.calls != 1 || !strings.Contains(response.Body.String(), `"requests":[]`) {
		t.Fatal("administrator metrics read failed", response.Code, metrics.calls, response.Body.String())
	}
}
