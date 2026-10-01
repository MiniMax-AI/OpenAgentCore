package modelprovider

import (
	"errors"
	"net/http"
	"slices"
	"testing"
)

func TestLookupRouteMatchesOnlyDeclaredRoutes(t *testing.T) {
	for _, header := range StrippedHeaders {
		if http.CanonicalHeaderKey(header) != header {
			t.Fatalf("stripped header %q is not canonical", header)
		}
	}
	for _, protocol := range []Protocol{Anthropic, Responses, ChatCompletions} {
		routes := Routes(protocol)
		if len(routes) == 0 {
			t.Fatalf("%s declares no routes", protocol)
		}
		credential, err := UpstreamCredential(protocol)
		if err != nil {
			t.Fatalf("%s declares no credential: %v", protocol, err)
		}
		if !slices.Contains(StrippedHeaders, credential.Header) {
			t.Fatalf("%s credential header %s is not stripped", protocol, credential.Header)
		}
		for _, route := range routes {
			if got, err := LookupRoute(protocol, route.Method, route.Path); err != nil || got != route {
				t.Fatalf("%s %s %s did not match: %v", protocol, route.Method, route.Path, err)
			}
			for _, path := range []string{route.Path + "/", route.Path + "x", "/v1/../" + route.Path[1:], "/" + route.Path} {
				if _, err := LookupRoute(protocol, route.Method, path); !errors.Is(err, ErrRouteNotFound) {
					t.Fatalf("%s %s matched: %v", protocol, path, err)
				}
			}
			if _, err := LookupRoute(protocol, "DELETE", route.Path); !errors.Is(err, ErrMethodNotAllowed) {
				t.Fatalf("%s DELETE %s: %v", protocol, route.Path, err)
			}
		}
	}
	for _, miss := range []struct {
		protocol     Protocol
		method, path string
		want         error
	}{
		{Anthropic, "GET", "/v1/models", ErrRouteNotFound},
		{Anthropic, "POST", "/v1/files", ErrRouteNotFound},
		{Anthropic, "post", "/v1/messages", ErrMethodNotAllowed},
		{Anthropic, "POST", "/responses", ErrRouteNotFound},
		{Responses, "POST", "/v1/messages", ErrRouteNotFound},
		{ChatCompletions, "POST", "/responses", ErrRouteNotFound},
		{"openai", "POST", "/responses", ErrConfiguration},
	} {
		if _, err := LookupRoute(miss.protocol, miss.method, miss.path); !errors.Is(err, miss.want) {
			t.Fatalf("%s %s %s = %v, want %v", miss.protocol, miss.method, miss.path, err, miss.want)
		}
	}
}

func TestPlaceholderPassesProviderValidation(t *testing.T) {
	for _, protocol := range []Protocol{Anthropic, Responses, ChatCompletions} {
		gateway := Provider{Protocol: protocol, BaseURL: "http://127.0.0.1:41000", APIKey: Placeholder, ContextWindow: 64000, MaxOutputTokens: 4096}
		if err := gateway.Validate(); err != nil {
			t.Fatalf("%s rejected the placeholder: %v", protocol, err)
		}
	}
}

func TestUpstreamPathJoinsBaseAndRoute(t *testing.T) {
	for _, join := range []struct{ base, route, want string }{
		{"", "/responses", "/responses"},
		{"/", "/responses", "/responses"},
		{"/v1", "/responses", "/v1/responses"},
		{"/v1/", "/responses", "/v1/responses"},
		{"/v1//", "/responses", "/v1/responses"},
		{"/anthropic", "/v1/messages", "/anthropic/v1/messages"},
		{"/a%2Fb/v1", "/chat/completions", "/a%2Fb/v1/chat/completions"},
	} {
		if got := UpstreamPath(join.base, join.route); got != join.want {
			t.Fatalf("UpstreamPath(%q, %q) = %q, want %q", join.base, join.route, got, join.want)
		}
	}
}
