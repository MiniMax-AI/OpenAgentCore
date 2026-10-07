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
	for _, tc := range []struct {
		name  string
		req   proto.PromptRequestPayload
		valid bool
	}{
		{"native owned", proto.PromptRequestPayload{}, true},
		{"native model", proto.PromptRequestPayload{Model: "fixture"}, true},
		{"explicit", proto.PromptRequestPayload{Model: "fixture", ModelProvider: provider()}, true},
		{"missing model", proto.PromptRequestPayload{ModelProvider: provider()}, false},
		{"blank model", proto.PromptRequestPayload{Model: "  "}, false},
		{"invalid provider", proto.PromptRequestPayload{Model: "fixture", ModelProvider: &modelprovider.Provider{Protocol: modelprovider.Responses}}, false},
		{"undeclared protocol", proto.PromptRequestPayload{Model: "fixture", ModelProvider: &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: "https://provider.example", APIKey: "private-sentinel"}}, false},
		{"empty native", proto.PromptRequestPayload{HarnessConfig: json.RawMessage(`{}`)}, true},
		{"null native", proto.PromptRequestPayload{HarnessConfig: json.RawMessage(`null`)}, false},
		{"array native", proto.PromptRequestPayload{HarnessConfig: json.RawMessage(`[]`)}, false},
		{"undeclared native", proto.PromptRequestPayload{HarnessConfig: json.RawMessage(`{"effort":"high"}`)}, false},
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
