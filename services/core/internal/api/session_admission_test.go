package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// forbidSessionAccess withdraws the Session and deployment model provider reads
// testHandler serves, so any access fails the test.
func forbidSessionAccess(_ *Dependencies, f *testFakes) {
	f.sessionCreation.createSession, f.sessionCreation.findSessionCreation = nil, nil
	f.sessions.getSession, f.sessions.listSessions = nil, nil
	f.modelProviders.resolve = nil
}

func TestSessionAdmissionRejectsBeforeResourceOrExecutionAccess(t *testing.T) {
	for _, environment := range []string{"none", "openai_hosted"} {
		for _, input := range []string{"", `,"input":null`} {
			for _, stream := range []bool{false, true} {
				if environment == "openai_hosted" && !stream {
					continue
				}
				t.Run(fmt.Sprintf("%s/%s/stream=%t", environment, input, stream), func(t *testing.T) {
					// Any resource access, including creation retry lookup, fails the test.
					handler, _, _ := testHandler(t, forbidSessionAccess, func(d *Dependencies, f *testFakes) { d.Execution = f.execution() })
					body := fmt.Sprintf(`{"agent":{"model":"example"},"environment":{"type":%q},"stream":%t%s}`, environment, stream, input)
					for _, token := range []string{"test-api-key", "invalid"} {
						request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(body))
						request.Header.Set("Authorization", "Bearer "+token)
						request.Header.Set("OpenAI-Beta", "agents=v1")
						request.Header.Set("Content-Type", "application/json")
						request.Header.Set("Idempotency-Key", "retained-creation-key")
						response := httptest.NewRecorder()
						handler.ServeHTTP(response, request)
						if token == "invalid" {
							if response.Code != http.StatusUnauthorized {
								t.Fatal(response.Code, response.Body)
							}
							continue
						}
						var failure v1.ErrorResponse
						if response.Code != http.StatusBadRequest || json.Unmarshal(response.Body.Bytes(), &failure) != nil || failure.Error.Code == nil || *failure.Error.Code != "invalid_request_error" || failure.Error.Type != "invalid_request_error" || failure.Error.Param != nil {
							t.Fatal(response.Code, response.Body)
						}
					}
				})
			}
		}
	}
}

func TestSessionEmptyUpdateRejectsBeforeResourceAccess(t *testing.T) {
	handler, _, _ := testHandler(t, forbidSessionAccess)
	for _, token := range []string{"test-api-key", "invalid"} {
		request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions/unknown", strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("OpenAI-Beta", "agents=v1")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if token == "invalid" {
			if response.Code != http.StatusUnauthorized {
				t.Fatal(response.Code, response.Body)
			}
			continue
		}
		var failure v1.ErrorResponse
		if response.Code != http.StatusBadRequest || json.Unmarshal(response.Body.Bytes(), &failure) != nil || failure.Error.Code == nil || *failure.Error.Code != "invalid_request_error" || failure.Error.Message != "At least one update field is required" {
			t.Fatal(response.Code, response.Body)
		}
	}
}
