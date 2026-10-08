package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

func TestInstallationReadNeedsOnlyTheCoreKey(t *testing.T) {
	deps, fakes := testDependencies(t)
	fakes.projectsReader.resolveAPIKey = projectKeys(t, callerBinding()).ResolveAPIKey
	deps.CoreKeys = coreKeys(t, "administrator")
	public, id := "https://core.example", "5b7c0f3e-0000-4000-8000-000000000001"
	settings := InstallationConfiguration{Settings: []InstallationSetting{{Key: "ports.core", Value: 8091, Default: 8091, Changeable: true, Restarts: []InstallationService{"core"}}}}
	fakes.installationBindings.addressBindings = func(context.Context) (deployment.AddressBindings, error) {
		return deployment.AddressBindings{Nodes: 2, NodesOnOtherAddress: 1}, nil
	}
	// No sandbox deployment: the read is available before any deployment exists.
	deps.Installation = Installation{InstallationID: id, PublicURL: public, APIBaseURL: public + "/v1", Configuration: settings}
	h := newTestHandler(t, deps)
	get := func(token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/core/v1/installation", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		result := httptest.NewRecorder()
		h.ServeHTTP(result, request)
		return result
	}
	if result := get("caller"); result.Code != http.StatusUnauthorized {
		t.Fatal("Project key read installation facts", result.Code)
	}
	result := get("administrator")
	var body map[string]any
	if result.Code != http.StatusOK || json.Unmarshal(result.Body.Bytes(), &body) != nil || body["object"] != "core.installation" ||
		body["public_url"] != public || body["address_bindings"].(map[string]any)["nodes_on_other_address"] != float64(1) ||
		body["configuration"].(map[string]any)["settings"].([]any)[0].(map[string]any)["key"] != "ports.core" {
		t.Fatal(result.Code, result.Body.String())
	}
}

func TestDeploymentAddressIsNotInput(t *testing.T) {
	deps, fakes := sandboxFakes(t)
	initializations := 0
	initialize := func(_ context.Context, input sandbox.Selection) (deployment.View, error) {
		initializations++
		if input.Provider != "e2b" || input.ExpectedGeneration != 0 {
			t.Fatal("invalid selection reached initialization", input.Provider, input.ExpectedGeneration)
		}
		return deployment.View{}, placement.ErrPublicURLUnreachable
	}
	// The strict fake fails the test if core_url reaches the update.
	fakes.deploymentChanges.initializeSandboxDeployment = initialize
	fakes.deployment.decodeConfiguration = providers.Builtin().DecodeInput
	h := newTestHandler(t, deps)
	for _, test := range []struct {
		method, body, code string
		status             int
	}{
		{http.MethodPost, `{"provider":"docker","core_url":"https://core.example","expected_generation":0}`, `"code":"invalid_request"`, http.StatusBadRequest},
		{http.MethodPut, `{"provider":"docker","core_url":"https://core.example","expected_generation":1}`, `"code":"invalid_request"`, http.StatusBadRequest},
		{http.MethodPost, `{"provider":"e2b","expected_generation":0,"credential":{"api_key":"key"},"configuration":{"template":"runtime:build"}}`, `"code":"sandbox_configuration_error"`, http.StatusConflict},
	} {
		request := httptest.NewRequest(test.method, "/core/v1/sandbox/deployment", strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer administrator")
		request.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		h.ServeHTTP(result, request)
		if result.Code != test.status || !strings.Contains(result.Body.String(), test.code) {
			t.Fatal(test.method, result.Code, result.Body.String())
		}
	}
	if initializations != 1 {
		t.Fatal("address rejection did not isolate initialization", initializations)
	}
}
