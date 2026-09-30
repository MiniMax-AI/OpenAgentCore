package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSystemPackagesRejectedAtEveryInput(t *testing.T) {
	for _, value := range []string{`null`, `[]`, `["jq"]`} {
		packages := `"packages":{"npm":["semver"],"python":["packaging"],"system":` + value + `}`
		t.Run(value, func(t *testing.T) {
			if _, err := decodeTemplateInput([]byte("{" + packages + "}")); !errors.Is(err, errSystemPackages) {
				t.Fatal("template accepted removed system packages")
			}
			raw := json.RawMessage(`{"type":"openai_hosted",` + packages + "}")
			if _, err := decodeSessionEnvironment(raw); !errors.Is(err, errSystemPackages) {
				t.Fatal("Session accepted removed system packages")
			}
			if _, err := storedEnvironment(raw); !errors.Is(err, errSystemPackages) {
				t.Fatal("stored public configuration silently ignored system packages")
			}
			reference := json.RawMessage(`{"type":"openai_hosted","environment_template_id":"saved",` + packages + "}")
			if _, _, _, err := decodeTemplateEnvironment(reference); !errors.Is(err, errSystemPackages) {
				t.Fatal("template override accepted removed system packages")
			}
		})
	}
	if _, err := decodeTemplateInput([]byte(`{"packages":{"npm":["semver"],"python":["packaging"]}}`)); err != nil {
		t.Fatal("supported package managers rejected", err)
	}
}

func TestSystemPackageErrorExplainsPreinstallation(t *testing.T) {
	h, _, _ := testHandler(t)
	for _, endpoint := range []string{"/v1/agents/environments/templates", "/v1/agents/sessions"} {
		body := `{"packages":{"system":["private-package-canary"]}}`
		if endpoint == "/v1/agents/sessions" {
			body = `{"agent":{"model":"test"},"environment":{"type":"openai_hosted","packages":{"system":["private-package-canary"]}}}`
		}
		req := httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-api-key")
		req.Header.Set("OpenAI-Beta", "agents=v1")
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		var result struct {
			Error struct {
				Param   string `json:"param"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusBadRequest || result.Error.Param != "packages.system" || result.Error.Message != errSystemPackages.message || strings.Contains(response.Body.String(), "private-package-canary") {
			t.Fatal(endpoint, response.Code, response.Body.String())
		}
	}
}
