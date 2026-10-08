package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// orcaConsole is a console with the OrcaRouter origins pointed at test servers.
// The catalog and the exchange accept the fake key and code used by the tests;
// no test value is a real credential.
func orcaConsole(t *testing.T, catalog, exchange http.HandlerFunc) *console {
	t.Helper()
	api := httptest.NewServer(catalog)
	auth := httptest.NewServer(exchange)
	t.Cleanup(api.Close)
	t.Cleanup(auth.Close)
	return &console{orca: orcaRouter{authOrigin: auth.URL, apiOrigin: api.URL, transport: http.DefaultTransport.(*http.Transport).Clone()}}
}

func serveOrca(h *console, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, body)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	h.serveOrcaRouter(recorder, request)
	return recorder
}

func TestOrcaCatalogUsesTheAPIOriginAndReducesRecords(t *testing.T) {
	var seen string
	h := orcaConsole(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path + "?" + r.URL.RawQuery
		if got := r.Header.Get("Authorization"); got != "Bearer sk-orca-fixture" {
			t.Errorf("Authorization = %q", got)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		writeJSON(t, w, map[string]any{"data": []any{
			map[string]any{"id": "openai/gpt-5.5", "name": "OpenAI: GPT-5.5", "context_length": 272000,
				"max_completion_tokens": 128000, "supported_endpoint_types": []string{"openai", "openai-response"},
				"architecture": map[string]any{"input_modalities": []string{"text", "image"}},
				"pricing":      map[string]any{"prompt": "0.000005"}},
			map[string]any{"id": "bad/" + strings.Repeat("x", 210), "context_length": -1},
			map[string]any{"name": "no id"},
			map[string]any{"id": "  "},
		}})
	}, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("catalog read must not call the auth origin")
	})
	recorder := serveOrca(h, http.MethodGet, "/console/orcarouter/catalog?capability=chat", nil, map[string]string{"X-OrcaRouter-Key": "sk-orca-fixture"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if seen != "/v1/models?capability=chat" {
		t.Errorf("catalog path = %q", seen)
	}
	var response orcaCatalogResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Degraded || len(response.Models) != 1 {
		t.Fatalf("models = %#v", response)
	}
	model := response.Models[0]
	if model.ID != "openai/gpt-5.5" || model.Name != "OpenAI: GPT-5.5" || model.ContextLength == nil || *model.ContextLength != 272000 {
		t.Fatalf("model = %#v", model)
	}
	if !model.ModalitiesDeclared || len(model.InputModalities) != 2 {
		t.Fatalf("modalities = %#v", model)
	}
	if strings.Contains(recorder.Body.String(), "pricing") || strings.Contains(recorder.Body.String(), "0.000005") {
		t.Fatalf("catalog leaked provider internals: %s", recorder.Body.String())
	}
}

func TestOrcaCatalogRefusesBadInputAndReportsOutages(t *testing.T) {
	h := orcaConsole(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}, func(w http.ResponseWriter, r *http.Request) {})
	for name, test := range map[string]struct {
		capability string
		key        string
		status     int
	}{
		"unknown capability": {"audio", "sk-orca-fixture", http.StatusBadRequest},
		"missing key":        {"chat", "", http.StatusBadRequest},
		"header injection":   {"chat", "sk-orca\r\nfixture", http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := serveOrca(h, http.MethodGet, "/console/orcarouter/catalog?capability="+test.capability, nil,
				map[string]string{"X-OrcaRouter-Key": test.key})
			if recorder.Code != test.status {
				t.Fatalf("status = %d", recorder.Code)
			}
		})
	}
	recorder := serveOrca(h, http.MethodGet, "/console/orcarouter/catalog?capability=chat", nil, map[string]string{"X-OrcaRouter-Key": "sk-orca-fixture"})
	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "degraded") {
		t.Fatalf("outage = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestOrcaCatalogSeparatesARejectedKeyFromAnOutage(t *testing.T) {
	h := orcaConsole(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"no"}}`, http.StatusUnauthorized)
	}, func(http.ResponseWriter, *http.Request) {})
	recorder := serveOrca(h, http.MethodGet, "/console/orcarouter/catalog?capability=chat", nil, map[string]string{"X-OrcaRouter-Key": "sk-orca-revoked"})
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "no") && strings.Contains(recorder.Body.String(), "error\":") {
		t.Fatalf("upstream body forwarded: %s", recorder.Body.String())
	}
}

func TestOrcaExchangePostsToTheAuthOriginAndRequiresS256(t *testing.T) {
	var seenPath, seenBody, seenOrigin string
	h := orcaConsole(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("exchange must not call the API origin")
	}, func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenOrigin = r.Header.Get("Origin")
		raw, _ := io.ReadAll(r.Body)
		seenBody = string(raw)
		writeJSON(t, w, map[string]any{"key": "sk-orca-issued", "user_id": "1", "scope": "api"})
	})
	recorder := serveOrca(h, http.MethodPost, "/console/orcarouter/exchange",
		strings.NewReader(`{"code":"one-time","code_verifier":"verifier-value","code_challenge_method":"S256"}`),
		map[string]string{"Content-Type": "application/json"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if seenPath != "/api/v1/auth/keys" {
		t.Fatalf("exchange path = %q", seenPath)
	}
	if seenOrigin != "" {
		t.Fatalf("exchange sent Origin %q", seenOrigin)
	}
	for _, want := range []string{"one-time", "verifier-value", `"code_challenge_method":"S256"`} {
		if !strings.Contains(seenBody, want) {
			t.Fatalf("exchange body %q missing %q", seenBody, want)
		}
	}
	var granted map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &granted); err != nil {
		t.Fatal(err)
	}
	if granted["key"] != "sk-orca-issued" || granted["scope"] != "api" {
		t.Fatalf("grant = %#v", granted)
	}

	for name, body := range map[string]string{
		"plain challenge":  `{"code":"c","code_verifier":"v","code_challenge_method":"plain"}`,
		"missing verifier": `{"code":"c","code_challenge_method":"S256"}`,
		"not json":         `not-json`,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := serveOrca(h, http.MethodPost, "/console/orcarouter/exchange", strings.NewReader(body),
				map[string]string{"Content-Type": "application/json"})
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
	recorder = serveOrca(h, http.MethodPost, "/console/orcarouter/exchange",
		strings.NewReader(`{"code":"c","code_verifier":"v","code_challenge_method":"S256"}`),
		map[string]string{"Content-Type": "text/plain"})
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestOrcaExchangeReportsUpstreamFailuresWithoutTheBody(t *testing.T) {
	for name, test := range map[string]struct {
		status int
		body   string
		want   int
	}{
		"expired or reused code":  {http.StatusForbidden, `{"error":"invalid_grant","error_description":"verifier sk-orca-secret"}`, http.StatusBadRequest},
		"downgraded method":       {http.StatusBadRequest, `{"error":"invalid_request"}`, http.StatusBadRequest},
		"rate limited":            {http.StatusTooManyRequests, `{"error":"slow_down"}`, http.StatusBadRequest},
		"unexpected server error": {http.StatusInternalServerError, `{"error":"boom"}`, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			h := orcaConsole(t, func(http.ResponseWriter, *http.Request) {},
				func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(test.status)
					_, _ = io.WriteString(w, test.body)
				})
			recorder := serveOrca(h, http.MethodPost, "/console/orcarouter/exchange",
				strings.NewReader(`{"code":"c","code_verifier":"v","code_challenge_method":"S256"}`),
				map[string]string{"Content-Type": "application/json"})
			if recorder.Code != test.want {
				t.Fatalf("status = %d", recorder.Code)
			}
			if strings.Contains(recorder.Body.String(), "sk-orca-secret") || strings.Contains(recorder.Body.String(), "invalid_grant") {
				t.Fatalf("upstream body forwarded: %s", recorder.Body.String())
			}
		})
	}
}

func TestOrcaExchangeRejectsAScopeItWasNotGranted(t *testing.T) {
	h := orcaConsole(t, func(http.ResponseWriter, *http.Request) {},
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"key": "sk-orca-issued", "scope": "connector"})
		})
	recorder := serveOrca(h, http.MethodPost, "/console/orcarouter/exchange",
		strings.NewReader(`{"code":"c","code_verifier":"v","code_challenge_method":"S256"}`),
		map[string]string{"Content-Type": "application/json"})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestOrcaRouterOriginsUseOverridesAndRequireHTTPSOffLoopback(t *testing.T) {
	for name, test := range map[string]struct {
		env  map[string]string
		auth string
		api  string
		ok   bool
	}{
		"defaults":          {map[string]string{}, defaultOrcaAuthOrigin, defaultOrcaAPIOrigin, true},
		"shared fallback":   {map[string]string{"ORCA_BASE_URL": "https://orca.internal"}, "https://orca.internal", "https://orca.internal", true},
		"explicit override": {map[string]string{"ORCA_BASE_URL": "https://orca.internal", "ORCA_AUTH_BASE_URL": "https://login.internal", "ORCA_API_BASE_URL": "https://relay.internal/"}, "https://login.internal", "https://relay.internal", true},
		"loopback http":     {map[string]string{"ORCA_BASE_URL": "http://127.0.0.1:8099"}, "http://127.0.0.1:8099", "http://127.0.0.1:8099", true},
		"remote http":       {map[string]string{"ORCA_BASE_URL": "http://orca.internal"}, "", "", false},
		"path rejected":     {map[string]string{"ORCA_AUTH_BASE_URL": "https://orca.internal/auth"}, "", "", false},
		"credentials":       {map[string]string{"ORCA_API_BASE_URL": "https://user:pass@orca.internal"}, "", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			router, err := loadOrcaRouter()
			if test.ok != (err == nil) {
				t.Fatalf("err = %v", err)
			}
			if !test.ok {
				return
			}
			if router.authOrigin != test.auth || router.apiOrigin != test.api {
				t.Fatalf("origins = %q %q", router.authOrigin, router.apiOrigin)
			}
			if got := router.exchangeURL(); got != test.auth+"/api/v1/auth/keys" {
				t.Fatalf("exchange = %q", got)
			}
		})
	}
}

func TestOrcaRouterCatalogURLKeepsTheVersionSegment(t *testing.T) {
	for _, test := range []struct{ origin, capability, want string }{
		{defaultOrcaAPIOrigin, "chat", defaultOrcaAPIOrigin + "/v1/models?capability=chat"},
		{"https://relay.internal", "", "https://relay.internal/v1/models"},
		{"https://relay.internal/v1", "embedding", "https://relay.internal/v1/models?capability=embedding"},
	} {
		router := orcaRouter{apiOrigin: test.origin}
		if got := router.catalogURL(test.capability); got == nil || got.String() != test.want {
			t.Fatalf("catalogURL(%q, %q) = %v", test.origin, test.capability, got)
		}
	}
	// The exchange path is the auth origin's `/api/v1/auth/keys`, never the
	// inference origin's `/v1/auth/keys`, which is a 404.
	for _, origin := range []string{defaultOrcaAuthOrigin, "https://login.internal"} {
		if got := (orcaRouter{authOrigin: origin}).exchangeURL(); got != origin+"/api/v1/auth/keys" {
			t.Fatalf("exchangeURL(%q) = %q", origin, got)
		}
	}
}

func TestOrcaCatalogNamesItsOriginAndKeepsTheKeyOutOfTheURL(t *testing.T) {
	var seen string
	h := orcaConsole(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.String()
		writeJSON(t, w, map[string]any{"data": []any{}})
	}, func(http.ResponseWriter, *http.Request) {})
	recorder := serveOrca(h, http.MethodGet, "/console/orcarouter/catalog?capability=chat", nil, map[string]string{"X-OrcaRouter-Key": "sk-orca-fixture"})
	var response orcaCatalogResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Origin != h.orca.apiOrigin {
		t.Fatalf("catalog origin = %q", response.Origin)
	}
	if strings.Contains(seen, "sk-orca-fixture") || strings.Contains(recorder.Body.String(), "sk-orca-fixture") {
		t.Fatalf("the key reached a URL or the response: %q %s", seen, recorder.Body.String())
	}
}

func TestOrcaConfigurationNamesTheDeploymentOrigins(t *testing.T) {
	h := orcaConsole(t, func(http.ResponseWriter, *http.Request) { t.Error("config must not read the API origin") },
		func(http.ResponseWriter, *http.Request) { t.Error("config must not read the auth origin") })
	recorder := serveOrca(h, http.MethodGet, "/console/orcarouter/config", nil, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var response map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["auth_origin"] != h.orca.authOrigin || response["api_origin"] != h.orca.apiOrigin {
		t.Fatalf("origins = %#v", response)
	}
	// The authorize URL belongs to the auth origin and is the fixed `/auth` path.
	if response["authorize_url"] != h.orca.authOrigin+"/auth" {
		t.Fatalf("authorize_url = %q", response["authorize_url"])
	}
	if response["authorize_url"] == response["api_origin"]+"/auth" {
		t.Fatal("the authorize URL must not be derived from the inference origin")
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode: %v", err)
	}
}
