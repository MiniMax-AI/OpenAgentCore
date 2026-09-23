package main

import (
	"encoding/json"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig"
	"github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig/builtin"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
)

func TestConfigurationCapabilitiesFollowDeployment(t *testing.T) {
	for _, gateway := range []bool{false, true} {
		got := coreStartupConfiguration("codex", []string{"codex"}, gateway, nil, "", nil).ConfigurationCapabilities
		if got.SchemaVersion != 1 || got.Scope != "core_build_provider_configuration" || got.RuntimeAvailability != "unknown" || len(got.Harnesses) != 3 {
			t.Fatalf("unexpected capabilities: %#v", got)
		}
		for _, harness := range got.Harnesses {
			if harness.Support != "supported" || harness.Enabled != (gateway && harness.Harness == "codex") || harness.Default != (harness.Harness == "codex") {
				t.Fatalf("deployment state confused with support: %#v", harness)
			}
			if len(harness.Providers) != 1 {
				t.Fatalf("missing provider: %#v", harness)
			}
			provider := harness.Providers[0]
			input := v1.ModelProviderInput{Protocol: provider.Protocol, BaseURL: "https://example.test", APIKey: "private-fixture"}
			needsLimits := len(provider.PositiveFields) > 0
			if (input.ValidateHarness(harness.Harness) != nil) != needsLimits {
				t.Fatalf("advertised requirements disagree with admission: %#v", harness)
			}
			want := []string{"protocol", "base_url", "api_key"}
			if needsLimits {
				want = append(want, "context_window", "max_output_tokens")
			}
			if !reflect.DeepEqual(provider.RequiredFields, want) {
				t.Fatalf("required fields = %v", provider.RequiredFields)
			}
		}
	}
}

func TestAdditionalAdapterAndUnknownDeclarationUseCommonProjection(t *testing.T) {
	declaration := harnessconfig.Configuration{Providers: []harnessconfig.Provider{{Protocol: "zeta"}, {Protocol: "alpha", RequiresTokenLimits: true}}}
	registry := harnessconfig.NewRegistry(map[string]harnessconfig.Configuration{"additional-adapter": declaration})
	catalog := engine.NewCatalog(map[string]engine.Profile{"undeclared": {}})
	configured := v1.CoreConfiguredStartupConfiguration{DefaultHarness: "undeclared", EnabledHarnesses: []string{"undeclared"}}
	got := configurationCapabilities(catalog, registry, configured)
	if len(got.Harnesses) != 2 || got.Harnesses[0].Harness != "additional-adapter" || got.Harnesses[0].Support != "supported" || got.Harnesses[0].Default || got.Harnesses[0].Enabled {
		t.Fatalf("additional registration was not projected: %#v", got)
	}
	if got.Harnesses[0].Providers[0].Protocol != "alpha" || got.Harnesses[0].Providers[1].Protocol != "zeta" || len(got.Harnesses[0].Providers[0].PositiveFields) != 2 {
		t.Fatal("provider order or requirements lost")
	}
	unknown := got.Harnesses[1]
	if unknown.Support != "unknown" || unknown.Providers == nil || len(unknown.Providers) != 0 {
		t.Fatalf("unknown support inferred: %#v", unknown)
	}
	input := &v1.ModelProviderInput{Protocol: "alpha", BaseURL: "https://example.test", APIKey: "fixture", ContextWindow: 100, MaxOutputTokens: 20}
	if input.ValidateHarnessWithRegistry("additional-adapter", registry) != nil || input.SafeView().ValidateHarnessWithRegistry("additional-adapter", registry) != nil {
		t.Fatal("discovered declaration did not validate through the shared admission path")
	}
	input.MaxOutputTokens = 0
	if input.ValidateHarnessWithRegistry("additional-adapter", registry) == nil || input.SafeView().ValidateHarnessWithRegistry("additional-adapter", registry) == nil {
		t.Fatal("advertised required limit was not enforced")
	}
	input.Protocol = "zeta"
	if input.ValidateHarnessWithRegistry("additional-adapter", registry) != nil {
		t.Fatal("protocol without required limits was rejected")
	}
	if input.ValidateHarness("additional-adapter") == nil {
		t.Fatal("test registry widened production provider admission")
	}
	if _, ok := catalog.Lookup("additional-adapter"); ok {
		t.Fatal("provider registration enabled an execution profile")
	}
	first, _ := json.Marshal(got)
	for i := 0; i < 10; i++ {
		next, _ := json.Marshal(configurationCapabilities(catalog, registry, configured))
		if string(first) != string(next) {
			t.Fatal("capabilities changed between reads")
		}
	}
}

func TestConfigurationAdmissionMatchesCorePolicy(t *testing.T) {
	admission := configurationCapabilities(engine.Catalog{}, builtin.Registry(), v1.CoreConfiguredStartupConfiguration{}).Admission
	if !reflect.DeepEqual(admission.CredentialEnvironmentTypes, []string{"openai_hosted"}) || !reflect.DeepEqual(admission.BaseURL.Schemes, []string{"https"}) || admission.BaseURL.UserInfo || admission.BaseURL.Query || admission.BaseURL.Fragment || admission.TokenLimits.Minimum != 0 || !admission.TokenLimits.MaxOutputNotAboveContext {
		t.Fatalf("unexpected Core restrictions: %#v", admission)
	}
	for _, environment := range []string{"none", "self_hosted", "openai_hosted", "unknown"} {
		if v1.ModelProviderEnvironmentSupported(environment) != (environment == "openai_hosted") {
			t.Fatal("credential environment admission disagrees")
		}
	}
	for _, base := range []string{"http://example.test", "https://user:private@example.test", "https://example.test?private=value", "https://example.test#private"} {
		input := v1.ModelProviderInput{Protocol: "responses", BaseURL: base, APIKey: "fixture"}
		if input.Validate() == nil {
			t.Fatal("advertised endpoint restriction not enforced")
		}
	}
	for _, limits := range [][2]int32{{-1, 0}, {100, -1}, {100, 101}} {
		input := v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://example.test", APIKey: "fixture", ContextWindow: limits[0], MaxOutputTokens: limits[1]}
		if input.Validate() == nil {
			t.Fatal("advertised token restriction not enforced")
		}
	}
}
