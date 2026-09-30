package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func assertConsoleCoreError(t *testing.T, body string, code string, status int) {
	t.Helper()
	var envelope struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatal(err, body)
	}
	kind := "invalid_request_error"
	if status >= 500 {
		kind = "server_error"
	}
	if len(envelope.Error) != 4 || envelope.Error["code"] != code || envelope.Error["param"] != nil || envelope.Error["type"] != kind {
		t.Fatal(body)
	}
}

type failingCoreTransport struct{ calls int }

func (transport *failingCoreTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls++
	return nil, errors.New("private provider URL https://private.invalid/?api_key=private-secret")
}

func TestCoreProxyFailureEnvelopeIsSafeAndNeverRetries(t *testing.T) {
	c := coreKeyConsoleConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected real transport") }))
	h := startConsole(t, c)
	cookie := signIn(t, h)
	transport := &failingCoreTransport{}
	h.proxy.Transport = transport
	out := authRequest(h, "GET", "/core/v1/projects", "", cookie)
	if out.Code != 502 || transport.calls != 1 || strings.Contains(out.Body.String(), "private") || out.Header().Get("Content-Type") != "application/json" {
		t.Fatal(out.Code, transport.calls, out.Body)
	}
	assertConsoleCoreError(t, out.Body.String(), "core_unreachable", 502)
}

func TestCoreProxyPreservesUpstreamEnvelopeAndAuthShape(t *testing.T) {
	const upstream = `{"error":{"message":"Refresh the configuration.","type":"conflict_error","code":"sandbox_generation_stale","param":"expected_generation","details":{"current_generation":3}}}`
	var calls atomic.Int32
	c := coreKeyConsoleConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(409)
		_, _ = w.Write([]byte(upstream))
	}))
	h := startConsole(t, c)
	cookie := signIn(t, h)
	out := authRequest(h, "GET", "/core/v1/sandbox/deployment", "", cookie)
	if out.Code != 409 || out.Body.String() != upstream || calls.Load() != 1 {
		t.Fatal(out.Code, out.Body, calls.Load())
	}
	for _, path := range []string{"/core", "/core/", "/core/v1", "/core/retired", "/v1/agents", "/api/v1/sandbox-node/configuration"} {
		out := authRequest(h, "GET", path, "", cookie)
		if out.Code != 404 || calls.Load() != 1 {
			t.Fatal("non-operation became proxyable", path, out.Code, calls.Load())
		}
	}
	request := httptest.NewRequest("GET", "/console/auth", nil)
	request.Host = "private-host.invalid"
	out = httptest.NewRecorder()
	h.ServeHTTP(out, request)
	if out.Code != 403 || out.Body.String() != "{\"error\":\"Forbidden\"}\n" {
		t.Fatal("auth shape changed", out.Code, out.Body)
	}
}
