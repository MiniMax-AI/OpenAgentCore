package harnessconfig

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

func TestPrepareModelConfiguration(t *testing.T) {
	c := Configuration{Providers: []Provider{{Protocol: "responses"}}}
	provider := func() *modelprovider.Provider {
		return &modelprovider.Provider{Protocol: modelprovider.Responses, BaseURL: "https://provider.example/v1", APIKey: "private-sentinel"}
	}
	with := func(native string) proto.PromptRequestPayload {
		return proto.PromptRequestPayload{Model: "fixture", ModelProvider: provider(), HarnessConfig: json.RawMessage(native)}
	}
	for _, tc := range []struct {
		name  string
		req   proto.PromptRequestPayload
		valid bool
	}{
		{"explicit", proto.PromptRequestPayload{Model: "fixture", ModelProvider: provider()}, true},
		{"missing provider", proto.PromptRequestPayload{Model: "fixture"}, false},
		{"missing model", proto.PromptRequestPayload{ModelProvider: provider()}, false},
		{"blank model", proto.PromptRequestPayload{Model: "  ", ModelProvider: provider()}, false},
		{"invalid provider", proto.PromptRequestPayload{Model: "fixture", ModelProvider: &modelprovider.Provider{Protocol: modelprovider.Responses}}, false},
		{"undeclared protocol", proto.PromptRequestPayload{Model: "fixture", ModelProvider: &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: "https://provider.example", APIKey: "private-sentinel"}}, false},
		{"empty native", with(`{}`), true},
		{"null native", with(`null`), false},
		{"array native", with(`[]`), false},
		{"undeclared native", with(`{"effort":"high"}`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Prepare(tc.req)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if err != nil && strings.Contains(err.Error(), "private-sentinel") {
				t.Fatal("secret leaked")
			}
			if tc.valid && got.HarnessConfig == nil {
				t.Fatal("missing owned native object")
			}
		})
	}
	req := proto.PromptRequestPayload{Model: "fixture", ModelProvider: provider()}
	got, err := c.Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	req.ModelProvider.APIKey = "changed"
	if got.Model != "fixture" || got.Provider.APIKey != "private-sentinel" {
		t.Fatal("prepared provider did not own snapshot")
	}
	if _, err := (Configuration{}).Prepare(proto.PromptRequestPayload{Model: "fixture", ModelProvider: provider()}); err == nil {
		t.Fatal("empty declaration inferred provider support")
	}
	if _, err := c.Prepare(proto.PromptRequestPayload{ModelProvider: provider()}); !errors.Is(err, ErrModel) {
		t.Fatal("model error was not shared")
	}
}

func TestConfigurationDeclarationRejectsUnknownAndDuplicateProtocols(t *testing.T) {
	for _, providers := range [][]Provider{
		{{Protocol: ""}},
		{{Protocol: "future-protocol"}},
		{{Protocol: "responses"}, {Protocol: "responses"}},
	} {
		c := Configuration{Providers: providers}
		if c.ValidateDeclaration() == nil {
			t.Fatal("invalid declaration accepted")
		}
	}
}

// The typed wire fields decode an explicit null or "" as absent, so Prepare
// must reject each of these decoded prompt requests.
func TestPrepareRejectsDecodedRequestsWithoutModelOrProvider(t *testing.T) {
	c := Configuration{Providers: []Provider{{Protocol: "responses"}}}
	provider := `{"protocol":"responses","base_url":"https://provider.example/v1","api_key":"private-sentinel"}`
	for payload, want := range map[string]error{
		`{"model":"m","model_provider":null}`:              ErrModelProvider,
		`{"model":"","model_provider":` + provider + `}`:   ErrModel,
		`{"model":null,"model_provider":` + provider + `}`: ErrModel,
	} {
		var req proto.PromptRequestPayload
		if err := (proto.Envelope{Type: proto.TypePromptRequest, Payload: json.RawMessage(payload)}).DecodeRequest(&req); err != nil {
			t.Fatalf("%s: %v", payload, err)
		}
		if _, err := c.Prepare(req); !errors.Is(err, want) {
			t.Fatalf("%s: err=%v, want %v", payload, err, want)
		}
	}
}
