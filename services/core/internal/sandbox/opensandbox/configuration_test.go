package opensandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestConfigurationRoundTripKeepsKeyOutOfPublicForms(t *testing.T) {
	a := ConfigurationAdapter{}
	c, err := a.DecodeInput(json.RawMessage(`{"api_url":"https://sandbox.example/","image":"registry.example/oac:1"}`), json.RawMessage(`{"api_key":"k-secret"}`))
	if err != nil || !c.ReplacesCredential() {
		t.Fatal(err)
	}
	s, err := a.Normalize(sandbox.Selection{DeploymentSpec: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 4096}, Network: sandbox.DefaultNetwork()}, Configuration: c})
	if err != nil {
		t.Fatal(err)
	}
	record, err := a.Encode(s.Configuration)
	if err != nil || string(record.Public) != `{"api_url":"https://sandbox.example","image":"registry.example/oac:1"}` || string(record.Secret) != "k-secret" {
		t.Fatalf("record = %s %q, %v", record.Public, record.Secret, err)
	}
	raw, _ := json.Marshal(s.Configuration)
	if text := string(raw) + fmt.Sprint(s.Configuration) + fmt.Sprintf("%+v", s.Configuration); strings.Contains(text, "k-secret") {
		t.Fatal("key leaked:", text)
	}
	back, err := a.Decode(record)
	if err != nil {
		t.Fatal(err)
	}
	if same, err := a.Equal(s.Configuration, back); err != nil || !same {
		t.Fatal("round trip differs", err)
	}
	// A retained generation's record holds no key until WithCredential composes it.
	record.Secret = nil
	if _, err := a.Decode(record); err != nil {
		t.Fatal("keyless record:", err)
	}
}

func TestConfigurationRejectsInvalidInput(t *testing.T) {
	a := ConfigurationAdapter{}
	if _, err := a.DecodeInput(json.RawMessage(`{"api_url":"https://s.example","image":"i","extra":"x"}`), nil); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("unknown field accepted")
	}
	if _, err := a.DecodeInput(json.RawMessage(`{"api_url":"https://s.example","image":"i"}`), json.RawMessage(`{"api_key":"k","extra":"x"}`)); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("unknown secret field accepted")
	}
	spec := sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 4096}, Network: sandbox.DefaultNetwork()}
	for _, c := range []DeploymentConfiguration{
		{APIURL: "ftp://s.example", Image: "i", APIKey: "k"}, {APIURL: "https://u:p@s.example", Image: "i", APIKey: "k"},
		{APIURL: "https://s.example/x", Image: "i", APIKey: "k"}, {APIURL: "https://s.example?x=1", Image: "i", APIKey: "k"},
		{APIURL: "https://s.example", Image: "has space", APIKey: "k"}, {APIURL: "https://s.example", Image: "i", APIKey: "a b"},
		{APIURL: "https://s.example", Image: "", APIKey: "k"}, {APIURL: "https://s.example", Image: "i"},
	} {
		if _, err := a.Normalize(sandbox.Selection{DeploymentSpec: spec, Configuration: &c}); !errors.Is(err, sandbox.ErrInvalid) {
			t.Fatalf("%+v accepted", c)
		}
	}
	if _, err := a.DiscoverConfiguration(t.Context(), sandbox.ConfigurationDiscoveryInput{}, sandbox.ProcessPaths{}); !errors.Is(err, providercontract.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := a.DiscoverSelection(t.Context(), sandbox.DirectConfig{}); !errors.Is(err, providercontract.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestResolveChangeInheritsKey(t *testing.T) {
	a := ConfigurationAdapter{}
	spec := sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 4096}, Network: sandbox.DefaultNetwork()}
	previous := sandbox.Selection{DeploymentSpec: spec, Configuration: &DeploymentConfiguration{APIURL: "https://s.example", Image: "i", APIKey: "old"}}
	next, err := a.ResolveChange(sandbox.Selection{DeploymentSpec: spec, Configuration: &DeploymentConfiguration{APIURL: "https://s.example", Image: "j"}}, previous)
	if c := configuration(next); err != nil || c.APIKey != "old" || c.ReplacesCredential() {
		t.Fatal("key not inherited", err)
	}
	if _, err := a.ResolveChange(sandbox.Selection{DeploymentSpec: spec, Configuration: &DeploymentConfiguration{APIURL: "https://s.example", Image: "j", CredentialSupplied: true}}, previous); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("explicit empty key inherited")
	}
}
