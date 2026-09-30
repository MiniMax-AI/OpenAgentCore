package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
)

// coreProviderValidationStore serves the real model configuration rules over
// storage that counts the deployment defaults reaching it.
type coreProviderValidationStore struct {
	t      testing.TB
	writes int
}

func (s *coreProviderValidationStore) Replace(context.Context, modelconfiguration.Record) (modelconfiguration.Configuration, error) {
	s.writes++
	return modelconfiguration.Configuration{}, nil
}

func (s *coreProviderValidationStore) Delete(context.Context, string) error {
	unexpectedCall(s.t, "Delete")
	return nil
}

func (s *coreProviderValidationStore) LoadSealed(context.Context, string) (modelconfiguration.Sealed, error) {
	unexpectedCall(s.t, "LoadSealed")
	return modelconfiguration.Sealed{}, nil
}

func (s *coreProviderValidationStore) configure(d *Dependencies, _ *testFakes) {
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		s.t.Fatal(err)
	}
	service, err := modelconfiguration.NewService(s, cipher)
	if err != nil {
		s.t.Fatal(err)
	}
	d.ModelProviders = service
}

func TestCoreModelProviderValidationFields(t *testing.T) {
	s := &coreProviderValidationStore{t: t}
	h, _, _ := adminTestHandler(t, s.configure)
	for _, tc := range []struct {
		name, harness, body, code, param string
		details                          map[string]any
	}{
		{"bundle", "codex", `{"api_key":"private-key"}`, "invalid_model_provider", "", nil},
		{"negative shape", "codex", `{"protocol":"responses","base_url":"http://private-url","api_key":"private-key","context_window":-1}`, "invalid_model_provider", "", nil},
		{"url first", "codex", `{"protocol":"private-protocol","base_url":"http://private-url","api_key":"private-key"}`, "model_provider_base_url_invalid", "base_url", nil},
		{"protocol", "codex", `{"protocol":"private-protocol","base_url":"https://example.test","api_key":"private-key"}`, "model_provider_protocol_unsupported", "protocol", map[string]any{"harness": "codex", "allowed_protocols": []any{"responses"}}},
		{"key", "codex", `{"protocol":"responses","base_url":"https://example.test","api_key":"private-key\n"}`, "model_provider_api_key_invalid", "api_key", map[string]any{"max_length": float64(16384)}},
		{"output", "codex", `{"protocol":"responses","base_url":"https://example.test","api_key":"private-key","context_window":1,"max_output_tokens":2}`, "model_provider_token_limits_invalid", "max_output_tokens", nil},
		{"context required", "mcode", `{"protocol":"anthropic","base_url":"https://example.test","api_key":"private-key"}`, "model_provider_token_limits_invalid", "context_window", nil},
		{"output required", "mcode", `{"protocol":"anthropic","base_url":"https://example.test","api_key":"private-key","context_window":2}`, "model_provider_token_limits_invalid", "max_output_tokens", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := projectKeyHTTP(h, http.MethodPut, "/core/v1/harnesses/"+tc.harness+"/model-configuration", "admin", `{"model":"fixture","model_provider":`+tc.body+`}`)
			var body struct {
				Error struct {
					Code    string
					Param   *string
					Details map[string]any
				}
			}
			if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			param := ""
			if body.Error.Param != nil {
				param = *body.Error.Param
			}
			if out.Code != 400 || body.Error.Code != tc.code || param != tc.param || !reflect.DeepEqual(body.Error.Details, tc.details) || strings.Contains(out.Body.String(), "private-") {
				t.Fatalf("unexpected diagnostics: %d %s", out.Code, out.Body)
			}
		})
	}
	if s.writes != 0 {
		t.Fatal("invalid input reached storage")
	}
	for _, tc := range []struct {
		harness, token string
		status         int
	}{{"codex", "", 401}, {"private-harness", "admin", 404}} {
		out := projectKeyHTTP(h, http.MethodPut, "/core/v1/harnesses/"+tc.harness+"/model-configuration", tc.token, `{}`)
		if out.Code != tc.status || strings.Contains(out.Body.String(), "private-") {
			t.Fatal("admission order changed", out.Code, out.Body)
		}
	}
}
