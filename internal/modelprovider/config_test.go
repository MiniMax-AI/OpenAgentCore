package modelprovider

import (
	"errors"
	"strings"
	"testing"
)

func TestProviderValidate(t *testing.T) {
	valid := Provider{Protocol: Responses, BaseURL: "https://model.example/api", APIKey: "fixture-upstream-key", ContextWindow: 64000, MaxOutputTokens: 4096}
	baseURL := func(u string) func(*Provider) { return func(p *Provider) { p.BaseURL = u } }
	anthropic := func(u string) func(*Provider) { return func(p *Provider) { p.Protocol, p.BaseURL = Anthropic, u } }
	apiKey := func(k string) func(*Provider) { return func(p *Provider) { p.APIKey = k } }
	limits := func(context, output int32) func(*Provider) {
		return func(p *Provider) { p.ContextWindow, p.MaxOutputTokens = context, output }
	}
	for _, tc := range []struct {
		name             string
		change           func(*Provider)
		strict, loopback string // the field rejected in each mode; empty when valid
	}{
		{"responses", func(*Provider) {}, "", ""},
		{"chat completions", func(p *Provider) { p.Protocol = ChatCompletions }, "", ""},
		{"anthropic", anthropic("https://model.example/anthropic"), "", ""},
		{"https loopback", baseURL("https://127.0.0.1:8443/v1"), "", ""},
		{"gateway handoff", func(p *Provider) { p.BaseURL, p.APIKey = "http://127.0.0.1:17101", Placeholder }, "base_url", ""},
		{"http IPv6 loopback", baseURL("http://[::1]:17101"), "base_url", ""},
		{"http loopback name", baseURL("http://localhost:17101"), "base_url", "base_url"},
		{"http remote", baseURL("http://model.example/v1"), "base_url", "base_url"},
		{"other scheme", baseURL("ftp://model.example"), "base_url", "base_url"},
		{"port", baseURL("https://model.example:8443/v1"), "", ""},
		{"IPv6 port", baseURL("https://[::1]:8443/v1"), "", ""},
		{"underscore", baseURL("https://model_gateway.internal/v1"), "", ""},
		{"IDN", baseURL("https://bücher.example/v1"), "", ""},
		{"no host", baseURL("https://"), "base_url", "base_url"},
		{"port above range", baseURL("https://model.example:99999/v1"), "base_url", "base_url"},
		{"port zero", baseURL("https://model.example:0/v1"), "base_url", "base_url"},
		{"empty punycode", baseURL("https://xn--.test"), "base_url", "base_url"},
		{"bad punycode", baseURL("https://xn--a.test"), "base_url", "base_url"},
		{"empty label", baseURL("https://a..b"), "base_url", "base_url"},
		{"numeric final label", baseURL("https://999.1.1.1"), "base_url", "base_url"},
		{"credentials", baseURL("https://user:secret@model.example/v1"), "base_url", "base_url"},
		{"query", baseURL("https://model.example/v1?key=secret"), "base_url", "base_url"},
		{"fragment", baseURL("https://model.example/v1#secret"), "base_url", "base_url"},
		{"newline", baseURL("https://model.example/v1\n"), "base_url", "base_url"},
		{"anthropic root", anthropic("https://model.example/"), "", ""},
		{"anthropic v1beta", anthropic("https://model.example/v1beta"), "", ""},
		{"anthropic version path", anthropic("https://model.example/v1"), "base_url", "base_url"},
		{"anthropic version path slash", anthropic("https://model.example/v1/"), "base_url", "base_url"},
		{"anthropic nested version path", anthropic("https://model.example/anthropic/v1//"), "base_url", "base_url"},
		{"alias protocol", func(p *Provider) { p.Protocol = "openai" }, "protocol", "protocol"},
		{"base URL before protocol", func(p *Provider) { p.Protocol, p.BaseURL = "openai", "http://model.example" }, "base_url", "base_url"},
		{"longest key", apiKey(strings.Repeat("k", MaxAPIKeyLength)), "", ""},
		{"long key", apiKey(strings.Repeat("k", MaxAPIKeyLength+1)), "api_key", "api_key"},
		{"blank key", apiKey(" \t"), "api_key", "api_key"},
		{"newline key", apiKey("fixture\nkey"), "api_key", "api_key"},
		{"NUL key", apiKey("fixture\x00key"), "api_key", "api_key"},
		{"protocol before key", func(p *Provider) { p.Protocol, p.APIKey = "openai", "" }, "protocol", "protocol"},
		{"no limits", limits(0, 0), "", ""},
		{"negative context", limits(-1, 0), "context_window", "context_window"},
		{"negative output", limits(10, -1), "max_output_tokens", "max_output_tokens"},
		{"output above context", limits(10, 11), "max_output_tokens", "max_output_tokens"},
		{"context before output", limits(-1, -2), "context_window", "context_window"},
		{"key before limits", func(p *Provider) { p.APIKey, p.ContextWindow = "", -1 }, "api_key", "api_key"},
		{"empty bundle", func(p *Provider) { *p = Provider{} }, "base_url", "base_url"},
	} {
		for loopbackHTTP, want := range map[bool]string{false: tc.strict, true: tc.loopback} {
			p := valid
			tc.change(&p)
			err := p.Validate(loopbackHTTP)
			var field *FieldError
			if want == "" && err != nil || want != "" && (!errors.As(err, &field) || field.Field != want) {
				t.Errorf("%s with loopback http %t: got %v, want rejected field %q", tc.name, loopbackHTTP, err, want)
			}
		}
	}
}
