package harnessconfig

import (
	"errors"
	"strings"
	"testing"
)

func TestPrepareModelConfiguration(t *testing.T) {
	c := Configuration{Providers: []Provider{{Protocol: "responses"}}}
	provider := func() map[string]any {
		return map[string]any{"protocol": "responses", "base_url": "https://provider.example/v1", "api_key": "private-sentinel"}
	}
	for _, tc := range []struct {
		name    string
		options map[string]any
		valid   bool
	}{
		{"native owned", nil, true},
		{"native model", map[string]any{"model": "fixture"}, true},
		{"explicit", map[string]any{"model": "fixture", "model_provider": provider()}, true},
		{"missing model", map[string]any{"model_provider": provider()}, false},
		{"empty model", map[string]any{"model": ""}, false},
		{"blank model", map[string]any{"model": "  "}, false},
		{"null model", map[string]any{"model": nil}, false},
		{"non string model", map[string]any{"model": 42}, false},
		{"null provider", map[string]any{"model": "fixture", "model_provider": nil}, false},
		{"null native", map[string]any{"harness_config": nil}, false},
		{"array native", map[string]any{"harness_config": []any{}}, false},
		{"undeclared native", map[string]any{"harness_config": map[string]any{"effort": "high"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Prepare(tc.options)
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
	got, err := c.Prepare(map[string]any{"model": "fixture", "model_provider": provider()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "fixture" {
		t.Fatal("prepared model changed")
	}
	if _, err := (Configuration{}).Prepare(map[string]any{"model": "fixture", "model_provider": provider()}); err == nil {
		t.Fatal("empty declaration inferred provider support")
	}
	if _, err := c.Prepare(map[string]any{"model_provider": provider()}); !errors.Is(err, ErrModel) {
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
