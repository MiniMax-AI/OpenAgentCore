package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestRestrictedNetworkPublicMetadataPreservesInput(t *testing.T) {
	domains := []string{"Example.com", "api.example.com", "example.com"}
	network := `{"access":"restricted","allowed_domains":["Example.com","api.example.com","example.com"]}`
	in, err := decodeTemplateInput([]byte(`{"network":` + network + `}`))
	if err != nil || !in.SetNetwork || !reflect.DeepEqual(in.AllowedDomains, domains) {
		t.Fatal("template input changed", in, err)
	}
	response := templateResponse(store.EnvironmentTemplate{NetworkAccess: in.NetworkAccess, AllowedDomains: in.AllowedDomains})
	if response.Network.Access != "restricted" || !reflect.DeepEqual(response.Network.AllowedDomains, domains) {
		t.Fatal("template response changed", response.Network)
	}
	raw := json.RawMessage(`{"type":"openai_hosted","network":` + network + `}`)
	env, err := decodeHostedEnvironment(raw)
	if err != nil || !reflect.DeepEqual(env.Network.AllowedDomains, domains) {
		t.Fatal("inline input changed", env, err)
	}
	session, err := hostedSessionEnvironment(store.Environment{ID: "environment", Configuration: raw})
	if err != nil || !reflect.DeepEqual(session.Network.AllowedDomains, domains) {
		t.Fatal("frozen Session metadata changed", session, err)
	}
	for _, invalid := range []string{
		`{"access":"restricted"}`, `{"access":"restricted","allowed_domains":null}`,
		`{"access":"restricted","allowed_domains":[]}`,
		`{"access":"restricted","allowed_domains":["*.example.com"]}`,
		`{"access":"restricted","allowed_domains":["https://example.com"]}`,
		`{"access":"restricted","allowed_domains":[null]}`,
		`{"access":"enabled","allowed_domains":["example.com"]}`,
	} {
		if _, err := decodeTemplateInput([]byte(`{"network":` + invalid + `}`)); err == nil {
			t.Fatal("invalid template network accepted", invalid)
		}
		if _, err := decodeHostedEnvironment(json.RawMessage(`{"type":"openai_hosted","network":` + invalid + `}`)); err == nil {
			t.Fatal("invalid inline network accepted", invalid)
		}
	}
}

func TestTemplateNetworkOverridesOnlyNarrowAndRetainIntent(t *testing.T) {
	lookup := &templateLookupStore{network: "restricted", domains: []string{"Example.com", "api.example.com", "example.com"}}
	h := Handler{store: lookup}
	for _, test := range []struct {
		name, override string
		want           []string
		invalid        bool
	}{
		{name: "inherit", want: lookup.domains},
		{name: "subset", override: `,"network":{"access":"restricted","allowed_domains":["API.EXAMPLE.COM"]}`, want: []string{"API.EXAMPLE.COM"}},
		{name: "same authority", override: `,"network":{"access":"restricted","allowed_domains":["example.com","api.example.com"]}`, want: []string{"example.com", "api.example.com"}},
		{name: "disabled", override: `,"network":{"access":"disabled"}`},
		{name: "enabled broadens", override: `,"network":{"access":"enabled"}`, invalid: true},
		{name: "subdomain broadens", override: `,"network":{"access":"restricted","allowed_domains":["other.example.com"]}`, invalid: true},
		{name: "foreign domain broadens", override: `,"network":{"access":"restricted","allowed_domains":["example.org"]}`, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var decoded decodedSessionRequest
			raw := `{"agent":{"model":"test"},"environment":{"type":"openai_hosted","environment_template_id":"saved"` + test.override + `}}`
			if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
				t.Fatal(err)
			}
			input, err := decoded.validated()
			if err != nil {
				t.Fatal(err)
			}
			before, err := sessionCreationRequest(input, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = h.resolveTemplateEnvironment(t.Context(), "tenant", &input)
			if test.invalid {
				if err == nil {
					t.Fatal("template authority widened")
				}
				return
			}
			if err != nil || !reflect.DeepEqual(input.Environment.Network.AllowedDomains, test.want) {
				t.Fatal("effective policy changed", input.Environment.Network, err)
			}
			after, err := sessionCreationRequest(input, nil)
			if err != nil || string(after) != string(before) {
				t.Fatal("resolution rewrote creation identity", string(before), string(after), err)
			}
		})
	}
}
