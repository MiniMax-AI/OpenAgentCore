package modelprovider

import (
	"errors"
	"testing"
)

func TestProviderValidate(t *testing.T) {
	valid := Provider{Protocol: Responses, BaseURL: "https://model.example/api", APIKey: "fixture-upstream-key", ContextWindow: 64000, MaxOutputTokens: 4096}
	for _, protocol := range []Protocol{Anthropic, Responses, ChatCompletions} {
		p := valid
		p.Protocol = protocol
		if err := p.Validate(); err != nil {
			t.Fatalf("%s rejected: %v", protocol, err)
		}
	}
	for name, change := range map[string]func(*Provider){
		"alias":           func(p *Provider) { p.Protocol = "openai" },
		"missing key":     func(p *Provider) { p.APIKey = "" },
		"newline key":     func(p *Provider) { p.APIKey = "fixture\nkey" },
		"URL credentials": func(p *Provider) { p.BaseURL = "https://user:secret@model.example/v1" },
		"remote HTTP":     func(p *Provider) { p.BaseURL = "http://model.example/v1" },
		"query":           func(p *Provider) { p.BaseURL = "https://model.example/v1?key=secret" },
		"fragment":        func(p *Provider) { p.BaseURL = "https://model.example/v1#secret" },
		"negative limit":  func(p *Provider) { p.ContextWindow = -1 },
		"excess output":   func(p *Provider) { p.MaxOutputTokens = 64001 },
		"empty bundle":    func(p *Provider) { *p = Provider{} },
	} {
		p := valid
		change(&p)
		if err := p.Validate(); !errors.Is(err, ErrConfiguration) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
}

func TestAnthropicBaseURLExcludesVersionPath(t *testing.T) {
	for _, tc := range []struct {
		protocol Protocol
		baseURL  string
		valid    bool
	}{
		{Anthropic, "https://model.example", true},
		{Anthropic, "https://model.example/", true},
		{Anthropic, "https://model.example/anthropic", true},
		{Anthropic, "https://model.example/v1beta", true},
		{Anthropic, "https://model.example/v1", false},
		{Anthropic, "https://model.example/v1/", false},
		{Anthropic, "https://model.example/anthropic/v1//", false},
		{Responses, "https://model.example/v1", true},
		{ChatCompletions, "https://model.example/v1/", true},
	} {
		err := Provider{Protocol: tc.protocol, BaseURL: tc.baseURL, APIKey: "fixture-upstream-key"}.Validate()
		if tc.valid != (err == nil) || err != nil && !errors.Is(err, ErrConfiguration) {
			t.Fatalf("%s %s: %v", tc.protocol, tc.baseURL, err)
		}
	}
}
