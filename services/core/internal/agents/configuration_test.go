package agents

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// testProvider is a saved provider view with its keys in normalized order.
const testProvider = `{"api_key_configured":true,"base_url":"https://example.test/v1","protocol":"responses"}`

func TestMergeConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, saved, patch, want string
	}{
		{"top-level field replaces", `{"model":"a","name":"old"}`, `{"name":"new"}`, `{"model":"a","name":"new"}`},
		{"empty patch keeps", `{"model":"a","x_agents_core":{"harness":"codex","harness_config":{"k":1}}}`, `{}`,
			`{"model":"a","x_agents_core":{"harness":"codex","harness_config":{"k":1}}}`},
		{"model change resets harness_config", `{"model":"a","x_agents_core":{"harness":"codex","harness_config":{"k":1}}}`, `{"model":"b"}`,
			`{"model":"b","x_agents_core":{"harness":"codex","harness_config":{}}}`},
		{"model change without saved extension", `{"model":"a"}`, `{"model":"b"}`, `{"model":"b","x_agents_core":{"harness_config":{}}}`},
		{"supplied harness_config wins", `{"model":"a","x_agents_core":{"harness":"codex","harness_config":{"k":1}}}`,
			`{"model":"b","x_agents_core":{"harness_config":{"k":2}}}`, `{"model":"b","x_agents_core":{"harness":"codex","harness_config":{"k":2}}}`},
		{"harness change merges subfields", `{"model":"a","x_agents_core":{"harness":"codex","model_provider":` + testProvider + `,"harness_config":{"k":1}}}`,
			`{"x_agents_core":{"harness":"claude_sdk"}}`,
			`{"model":"a","x_agents_core":{"harness":"claude_sdk","harness_config":{},"model_provider":` + testProvider + `}}`},
		{"provider change resets harness_config", `{"model":"a","x_agents_core":{"harness":"codex","harness_config":{"k":1}}}`,
			`{"x_agents_core":{"model_provider":null}}`, `{"model":"a","x_agents_core":{"harness":"codex","harness_config":{},"model_provider":null}}`},
		{"null extension replaces it", `{"model":"a","x_agents_core":{"harness":"codex"}}`, `{"model":"b","x_agents_core":null}`,
			`{"model":"b","x_agents_core":null}`},
		{"extension onto null", `{"model":"a","x_agents_core":null}`, `{"x_agents_core":{"harness":"codex"}}`,
			`{"model":"a","x_agents_core":{"harness":"codex","harness_config":{}}}`},
		{"large numbers survive", `{"model":"a","x":12345678901234567890}`, `{"y":1.50}`, `{"model":"a","x":12345678901234567890,"y":1.50}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patch := decodeTestPatch(t, tc.patch)
			before, _ := json.Marshal(patch)
			got, err := mergeConfiguration(json.RawMessage(tc.saved), patch)
			if err != nil || string(got) != tc.want {
				t.Fatalf("merge = %s, %v; want %s", got, err, tc.want)
			}
			if after, _ := json.Marshal(patch); string(after) != string(before) {
				t.Fatalf("merge changed the patch: %s", after)
			}
		})
	}
}

func TestMergeConfigurationRejects(t *testing.T) {
	big := `"` + strings.Repeat("x", 400*1024) + `"`
	for _, tc := range []struct {
		name, saved, patch string
		invalid            bool
	}{
		{"merged size bound", `{"model":"a","instructions":` + big + `}`, `{"name":` + `"` + strings.Repeat("y", 150*1024) + `"}`, true},
		{"extension not an object", `{"model":"a"}`, `{"x_agents_core":[]}`, true},
		{"corrupt saved configuration", `[]`, `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mergeConfiguration(json.RawMessage(tc.saved), decodeTestPatch(t, tc.patch))
			if err == nil || errors.Is(err, ErrInvalidInput) != tc.invalid {
				t.Fatalf("merge error = %v", err)
			}
		})
	}
}

func TestCreateConfigurationAndPatchBounds(t *testing.T) {
	oversized := json.RawMessage(`{"model":"` + strings.Repeat("x", MaxConfigurationBytes) + `"}`)
	for _, raw := range []string{"", "null", "[]", "true", `{"model":"x"`, `{} {}`, string(oversized)} {
		if _, err := createConfiguration(json.RawMessage(raw)); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("createConfiguration(%.20q) = %v", raw, err)
		}
	}
	if got, err := createConfiguration(json.RawMessage(`{ "model" : "x", "n": 1.0 }`)); err != nil || string(got) != `{"model":"x","n":1.0}` {
		t.Fatalf("createConfiguration = %s, %v", got, err)
	}
	for _, raw := range []string{"[]", "null", `{} {}`, string(oversized)} {
		if _, err := decodePatch(json.RawMessage(raw)); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("decodePatch(%.20q) = %v", raw, err)
		}
	}
	if patch, err := decodePatch(nil); err != nil || len(patch) != 0 {
		t.Fatalf("empty patch = %v, %v", patch, err)
	}
}

func TestValidateModelExecution(t *testing.T) {
	bundle := &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://example.test/v1", APIKey: "secret"}
	for _, tc := range []struct {
		name, configuration string
		provider            *v1.ModelProviderInput
		valid               bool
	}{
		{"no extension", `{"model":"a"}`, nil, true},
		{"native provider", `{"model":"a","x_agents_core":{"harness":"codex","model_provider":` + testProvider + `}}`, bundle, true},
		{"provider without harness", `{"model":"a","x_agents_core":{"model_provider":` + strings.Replace(testProvider, "responses", "anthropic", 1) + `}}`, nil, true},
		{"incompatible saved provider", `{"model":"a","x_agents_core":{"harness":"claude_sdk","model_provider":` + testProvider + `}}`, nil, false},
		{"invalid bundle", `{"model":"a"}`, &v1.ModelProviderInput{Protocol: "responses"}, false},
		{"invalid harness_config", `{"model":"a","x_agents_core":{"harness":"codex","harness_config":{"unknown_option":true}}}`, nil, false},
		{"extension not an object", `{"model":"a","x_agents_core":[]}`, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateModelExecution(json.RawMessage(tc.configuration), tc.provider)
			if (err == nil) != tc.valid || err != nil && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("validateModelExecution = %v", err)
			}
		})
	}
}

func TestListQueryValidate(t *testing.T) {
	for limit, valid := range map[int]bool{-1: false, 0: false, 1: true, MaxPageSize: true, MaxPageSize + 1: false} {
		if err := (ListQuery{Limit: limit}).Validate(); (err == nil) != valid || err != nil && !errors.Is(err, ErrInvalidInput) {
			t.Errorf("limit %d: %v", limit, err)
		}
	}
}

func TestReviseKeepsOrReplacesMetadata(t *testing.T) {
	current := Agent{Metadata: map[string]string{"team": "core"}, Configuration: json.RawMessage(`{"model":"a"}`)}
	kept, err := revise(current, map[string]json.RawMessage{}, nil, nil)
	if err != nil || string(kept.Metadata) != `{"team":"core"}` || kept.ModelProvider != nil {
		t.Fatalf("kept = %+v, %v", kept, err)
	}
	change := &ModelProviderChange{}
	replaced, err := revise(current, map[string]json.RawMessage{}, json.RawMessage(`{}`), change)
	if err != nil || string(replaced.Metadata) != `{}` || replaced.ModelProvider != change {
		t.Fatalf("replaced = %+v, %v", replaced, err)
	}
}

func decodeTestPatch(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	patch, err := decodePatch(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	return patch
}
