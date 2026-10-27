package execution

import (
	"errors"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestEgressFollowsTheCapAndTheModelProviderHost(t *testing.T) {
	cap := sandbox.Network{Public: false, Domains: []string{"gitlab.example.com"}}
	for _, tc := range []struct {
		baseURL string
		want    sandbox.Egress
	}{
		{"https://API.Model.example:8443/v1", sandbox.Egress{PrivateDomains: []string{"gitlab.example.com"}, Domains: []string{}, Endpoints: []string{"api.model.example"}}},
		{"http://10.1.2.3:8000/v1", sandbox.Egress{PrivateDomains: []string{"gitlab.example.com"}, Domains: []string{}, Endpoints: []string{"10.1.2.3"}}},
		{"http://127.0.0.1:8000/v1", sandbox.Egress{PrivateDomains: []string{"gitlab.example.com"}, Domains: []string{}, Endpoints: []string{}}},
		{"http://localhost:8000", sandbox.Egress{PrivateDomains: []string{"gitlab.example.com"}, Domains: []string{}, Endpoints: []string{}}},
		{"", sandbox.Egress{PrivateDomains: []string{"gitlab.example.com"}, Domains: []string{}, Endpoints: []string{}}},
	} {
		got, err := egressFor(cap, nil, tc.baseURL)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%q: %+v %v, want %+v", tc.baseURL, got, err, tc.want)
		}
	}
	if got, _ := egressFor(sandbox.DefaultNetwork(), &v1.EnvironmentNetworkInput{Access: "enabled"}, ""); !got.Public || len(got.PrivateDomains) != 0 {
		t.Fatalf("default cap: %+v", got)
	}
}

func TestEnvironmentPolicyNarrowsTheCap(t *testing.T) {
	open := sandbox.Network{Public: true, Domains: []string{"gitlab.example.com"}}
	closed := sandbox.Network{Public: false, Domains: []string{"gitlab.example.com"}}
	disabled := &v1.EnvironmentNetworkInput{Access: "disabled"}
	restricted := &v1.EnvironmentNetworkInput{Access: "restricted", AllowedDomains: []string{"GitLab.example.com", "pypi.org", "pypi.org"}}

	got, err := egressFor(open, disabled, "https://model.example/v1")
	if want := (sandbox.Egress{PrivateDomains: []string{}, Domains: []string{}, Endpoints: []string{"model.example"}}); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("disabled: %+v %v", got, err)
	}
	got, err = egressFor(open, restricted, "")
	if want := (sandbox.Egress{PrivateDomains: []string{"gitlab.example.com"}, Domains: []string{"pypi.org"}, Endpoints: []string{}}); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("restricted: %+v %v", got, err)
	}

	// A domain outside the cap fails with a typed error naming the entry, at admission and at allocation alike.
	var typed *sandbox.ConfigurationError
	if _, err = egressFor(closed, restricted, ""); !errors.As(err, &typed) || typed.Param != "environment.network.allowed_domains[1]" {
		t.Fatalf("outside the cap: %v", err)
	}
	if err := admitNetwork(sandbox.NetworkSupport{Restrict: true}, closed, restricted); !errors.As(err, &typed) || typed.Code != "sandbox_network_outside_cap" {
		t.Fatalf("admission: %v", err)
	}
}

func TestAdmissionNeedsProviderSupport(t *testing.T) {
	var typed *sandbox.ConfigurationError
	for _, policy := range []*v1.EnvironmentNetworkInput{{Access: "disabled"}, {Access: "restricted", AllowedDomains: []string{"pypi.org"}}} {
		if err := admitNetwork(sandbox.NetworkSupport{}, sandbox.DefaultNetwork(), policy); !errors.As(err, &typed) || typed.Param != "environment.network.access" {
			t.Fatalf("%+v: %v", policy, err)
		}
		if err := admitNetwork(sandbox.NetworkSupport{Restrict: true}, sandbox.DefaultNetwork(), policy); err != nil {
			t.Fatalf("%+v: %v", policy, err)
		}
	}
	if err := admitNetwork(sandbox.NetworkSupport{}, sandbox.DefaultNetwork(), nil); err != nil {
		t.Fatal(err)
	}
}
