package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestResourceNotFoundErrorSurfaces(t *testing.T) {
	for _, path := range []string{
		"/v1/agents/missing", "/v1/vaults/missing",
		"/v1/agents/sessions/missing/items", "/v1/agents/environments/missing/files",
		"/v1/files", "/v1/files/missing/content", "/v1/skills", "/v1/skills/missing/versions/1",
	} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, path, nil)
			writeStoreError(response, request, fmt.Errorf("lookup: %w", store.ErrNotFound))
			var body v1.ErrorResponse
			if response.Code != http.StatusNotFound || json.Unmarshal(response.Body.Bytes(), &body) != nil {
				t.Fatalf("response = %d %s", response.Code, response.Body)
			}
			beta := path == "/v1/agents/missing" || path == "/v1/vaults/missing" || path == "/v1/agents/sessions/missing/items" || path == "/v1/agents/environments/missing/files"
			if beta {
				if body.Error.Type != "not_found_error" || body.Error.Code == nil || *body.Error.Code != "not_found_error" {
					t.Fatalf("beta error = %s", response.Body)
				}
			} else if body.Error.Type != "invalid_request_error" || body.Error.Code != nil {
				t.Fatalf("non-beta error = %s", response.Body)
			}
			var raw map[string]json.RawMessage
			_ = json.Unmarshal(response.Body.Bytes(), &raw)
			var envelope map[string]json.RawMessage
			_ = json.Unmarshal(raw["error"], &envelope)
			if _, present := envelope["code"]; !present {
				t.Fatal("nullable error code must remain present")
			}
		})
	}
}

// An unresolved list cursor keeps its store message; Skill versions use the
// observed invalid_value code on after, Beta lists invalid_request_error with a
// null param.
func TestInvalidCursorErrorFields(t *testing.T) {
	for path, want := range map[string]string{
		"/v1/agents/sessions/session/items":         `{"error":{"message":"Invalid session item ID in ` + "`after`" + `","type":"invalid_request_error","code":"invalid_request_error","param":null}}`,
		"/v1/agents/sessions/session/subagents":     `{"error":{"message":"Invalid session item ID in ` + "`after`" + `","type":"invalid_request_error","code":"invalid_request_error","param":null}}`,
		"/v1/skills/skill_missing/versions":         `{"error":{"message":"Invalid session item ID in ` + "`after`" + `","type":"invalid_request_error","code":"invalid_value","param":"after"}}`,
		"/v1/agents/sessions/session/artifacts?x=1": `{"error":{"message":"Invalid session item ID in ` + "`after`" + `","type":"invalid_request_error","code":"invalid_request_error","param":null}}`,
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		writeStoreError(response, request, fmt.Errorf("list: %w", &store.InvalidCursorError{Message: "Invalid session item ID in `after`"}))
		if response.Code != http.StatusBadRequest || response.Body.String() != want+"\n" {
			t.Errorf("%s: %d %s", path, response.Code, response.Body)
		}
	}
}

// The constant Beta check runs before authentication on the Beta group
// (HP-05): a missing Beta header is 400 invalid_beta with or without valid
// credentials, and only a request carrying it reaches the 401.
func TestMissingBetaErrorBeforeAuthentication(t *testing.T) {
	for _, test := range []struct {
		name, authorization, beta string
		status                    int
		kind                      string
		code                      *string
	}{
		{"no credentials", "", "", http.StatusBadRequest, "invalid_beta", ptr("invalid_beta")},
		{"invalid credentials", "Bearer wrong", "", http.StatusBadRequest, "invalid_beta", ptr("invalid_beta")},
		{"valid credentials", "Bearer test-api-key", "", http.StatusBadRequest, "invalid_beta", ptr("invalid_beta")},
		{"beta without credentials", "", "agents=v1", http.StatusUnauthorized, "invalid_request_error", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, _, _ := testHandler(t)
			request := httptest.NewRequest(http.MethodGet, "/v1/agents/sessions", nil)
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			if test.beta != "" {
				request.Header.Set("OpenAI-Beta", test.beta)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var body v1.ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != test.status || body.Error.Type != test.kind || !equalOptional(body.Error.Code, test.code) || body.Error.Param != nil {
				t.Fatalf("%d %s", response.Code, response.Body)
			}
		})
	}
}

func ptr(value string) *string { return &value }

func equalOptional(got, want *string) bool {
	return (got == nil) == (want == nil) && (got == nil || *got == *want)
}

// Session deletion conflicts use the observed official 409 fields.
func TestSessionDeletionConflictError(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/v1/agents/sessions/session", nil)
	writeStoreError(response, request, fmt.Errorf("delete: %w", store.ErrSessionNotIdle))
	want := `{"error":{"message":"session must be durably idle or failed without required actions before deletion","type":"conflict_error","code":"conflict_error","param":null}}` + "\n"
	if response.Code != http.StatusConflict || response.Body.String() != want {
		t.Fatalf("response = %d %s", response.Code, response.Body)
	}
}

// Every 409 has type conflict_error (ERR-27). Core-only conflicts keep their
// documented local code; outside Session input, a Turn conflict keeps
// turn_conflict because the official behavior there is unobserved.
func TestConflictErrorsUseConflictType(t *testing.T) {
	for err, code := range map[error]string{
		store.ErrSandboxDeploymentConflict:     "sandbox_deployment_conflict",
		store.ErrRuntimeNodeInUse:              "runtime_node_in_use",
		store.ErrRuntimeLocalNodeConfigured:    "runtime_local_node_configured",
		store.ErrEnvironmentUnavailable:        "environment_unavailable",
		execution.ErrEnvironmentInputExpired:   "environment_input_expired",
		execution.ErrEnvironmentInputCancelled: "environment_input_cancelled",
		store.ErrSessionNotIdle:                "conflict_error",
		store.ErrFunctionResultConflict:        "conflict_error",
		store.ErrIdempotencyConflict:           "idempotency_conflict",
		store.ErrTurnConflict:                  "turn_conflict",
		store.ErrSessionInputPending:           "turn_conflict",
	} {
		t.Run(code, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/agents/environments/environment/files", nil)
			writeStoreError(response, request, fmt.Errorf("operation: %w", err))
			var body v1.ErrorResponse
			if response.Code != http.StatusConflict || json.Unmarshal(response.Body.Bytes(), &body) != nil ||
				body.Error.Type != "conflict_error" || body.Error.Code == nil || *body.Error.Code != code || body.Error.Param != nil {
				t.Fatalf("%v: %d %s", err, response.Code, response.Body)
			}
		})
	}
	response := httptest.NewRecorder()
	writeError(response, http.StatusConflict, "runtime_history_unsupported", "Runtime history is not supported for this Session.")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"type":"conflict_error","code":"runtime_history_unsupported"`) {
		t.Fatal(response.Body)
	}
}
