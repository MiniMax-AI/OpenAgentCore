package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestModelConfigurationRouteAdmission(t *testing.T) {
	for _, tc := range []struct{ name, protocol, native, model, code, param string }{
		{"invalid model", "responses", "{}", "", "model_configuration_model_invalid", "model"},
		{"reserved parameter", "responses", `{"api_key":"native-secret"}`, "model", "harness_config_invalid", "harness_config"},
		{"unknown parameter", "responses", `{"unknown":"native-secret"}`, "model", "harness_config_invalid", "harness_config"},
		{"non native route", "anthropic", `{"model_reasoning_effort":"high"}`, "model", "model_provider_protocol_unsupported", "protocol"},
		{"null", "responses", "null", "model", "invalid_model_provider", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &coreProviderValidationStore{t: t}
			h, _, _ := adminTestHandler(t, s.configure)
			body := `{"model":` + mustJSONForTest(tc.model) + `,"model_provider":{"protocol":"` + tc.protocol + `","base_url":"https://example.test","api_key":"provider-secret"},"harness_config":` + tc.native + `}`
			out := projectKeyHTTP(h, http.MethodPut, "/core/v1/harnesses/codex/model-configuration", "admin", body)
			var result struct {
				Error struct {
					Code  string
					Param *string
				}
			}
			if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			param := ""
			if result.Error.Param != nil {
				param = *result.Error.Param
			}
			if out.Code != 400 || result.Error.Code != tc.code || param != tc.param || s.writes != 0 || strings.Contains(out.Body.String(), "secret") {
				t.Fatalf("status=%d body=%s writes=%d", out.Code, out.Body, s.writes)
			}
		})
	}
}

func mustJSONForTest(value string) string { raw, _ := json.Marshal(value); return string(raw) }
