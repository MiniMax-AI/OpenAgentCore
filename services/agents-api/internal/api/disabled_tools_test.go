package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDisabledToolConfigurationRoundTrip(t *testing.T) {
	for _, raw := range []string{
		`{"type":"programmatic_tool_calling","enabled":false}`,
		`{"type":"web_search","mode":"disabled"}`,
		`{"type":"web_search","mode":"disabled","context_size":"high","allowed_domains":[],"location":{"city":"Paris","country":null}}`,
		`{"type":"web_search","mode":"disabled","context_size":null,"allowed_domains":["example.com"],"location":null}`,
	} {
		saved, err := resolveSavedTools([]json.RawMessage{json.RawMessage(raw)})
		if err != nil {
			t.Fatal(raw, err)
		}
		inline, err := resolveSessionTools([]json.RawMessage{json.RawMessage(raw)})
		if err != nil || string(saved[0]) != string(inline[0]) {
			t.Fatal(raw, saved, inline, err)
		}
		referenced, err := resolveSessionTools(saved)
		if err != nil || string(referenced[0]) != string(saved[0]) {
			t.Fatal("saved configuration changed on resolution", err)
		}
		if strings.Contains(raw, "web_search") && !strings.Contains(string(saved[0]), `"context_size":`) {
			t.Fatal("missing resource default")
		}
		if strings.Contains(raw, `"allowed_domains":[]`) && !strings.Contains(string(saved[0]), `"allowed_domains":[]`) {
			t.Fatal("empty domain list lost")
		}
	}
}

func TestDisabledToolAdmissionPrecedesPersistence(t *testing.T) {
	for _, tools := range []string{
		`[{"type":"programmatic_tool_calling"}]`,
		`[{"type":"programmatic_tool_calling","enabled":true}]`,
		`[{"type":"programmatic_tool_calling","enabled":null}]`,
		`[{"type":"programmatic_tool_calling","enabled":"false"}]`,
		`[{"type":"programmatic_tool_calling","enabled":false,"extra":true}]`,
		`[{"type":"programmatic_tool_calling","enabled":false},{"type":"programmatic_tool_calling","enabled":false}]`,
		`[{"type":"web_search","mode":"live"}]`,
		`[{"type":"web_search","mode":"cached"}]`,
		`[{"type":"web_search","mode":"disabled","allowed_domains":[null]}]`,
		`[{"type":"web_search","mode":"disabled","context_size":"enormous"}]`,
		`[{"type":"web_search","mode":"disabled","location":{"extra":true}}]`,
		`[{"type":"web_search","mode":"disabled"},{"type":"web_search","mode":"disabled"}]`,
	} {
		h, store, _ := testHandler(t)
		req := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(`{"agent":{"model":"model","tools":`+tools+`},"environment":{"type":"none"}}`))
		req.Header.Set("Authorization", "Bearer test-api-key")
		req.Header.Set("OpenAI-Beta", "agents=v1")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		if response.Code != 400 || store.tenant != "" {
			t.Fatal(tools, response.Code, response.Body)
		}
	}
}
