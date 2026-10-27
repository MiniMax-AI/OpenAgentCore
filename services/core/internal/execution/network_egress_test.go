package execution

import (
	"context"
	"errors"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestEgressLimitsTheModelProviderToItsPort(t *testing.T) {
	closed := sandbox.Network{Public: false, Domains: []string{"gitlab.example.com", "llm.corp.example"}}
	disabled := &v1.EnvironmentNetworkInput{Access: "disabled"}
	for _, tc := range []struct {
		name    string
		network sandbox.Network
		policy  *v1.EnvironmentNetworkInput
		baseURL string
		want    sandbox.Egress
	}{
		{"public host, default port", closed, nil, "https://API.Model.example/v1", sandbox.Egress{PrivateDomains: []string{"gitlab.example.com", "llm.corp.example"}, Domains: []string{}, Endpoints: []string{"api.model.example:443"}}},
		{"public host, http and own port", sandbox.DefaultNetwork(), disabled, "http://api.model.example:8000", sandbox.Egress{PrivateDomains: []string{}, Domains: []string{}, Endpoints: []string{"api.model.example:8000"}}},
		{"private host listed by the cap stays reachable when disabled", closed, disabled, "http://llm.corp.example:8000/v1", sandbox.Egress{PrivateDomains: []string{"llm.corp.example"}, Domains: []string{}, Endpoints: []string{"llm.corp.example:8000"}}},
		{"private host the cap does not list", sandbox.DefaultNetwork(), disabled, "http://llm.corp.example:8000/v1", sandbox.Egress{PrivateDomains: []string{}, Domains: []string{}, Endpoints: []string{"llm.corp.example:8000"}}},
		{"private IP literal", closed, disabled, "http://10.1.2.3:8000/v1", sandbox.Egress{PrivateDomains: []string{}, Domains: []string{}, Endpoints: []string{"10.1.2.3:8000"}}},
		{"loopback", closed, disabled, "http://127.0.0.1:8000/v1", sandbox.Egress{PrivateDomains: []string{}, Domains: []string{}, Endpoints: []string{}}},
		{"localhost", closed, disabled, "http://localhost:8000", sandbox.Egress{PrivateDomains: []string{}, Domains: []string{}, Endpoints: []string{}}},
	} {
		got, err := egressFor(tc.network, tc.policy, tc.baseURL)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: %+v %v, want %+v", tc.name, got, err, tc.want)
		}
	}
}

// A Session whose record is missing fails the attempt instead of falling back to the full cap.
func TestAllocationEgressFailsClosedWithoutSession(t *testing.T) {
	r := &runtimeLifecycle{sessions: missingSession{}}
	if _, err := r.allocationEgress(context.Background(), deployment.Allocation{}, sandbox.DefaultNetwork()); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}

type missingSession struct{ sessions.Reader }

func (missingSession) GetSession(context.Context, string, string) (sessions.Session, error) {
	return sessions.Session{}, sessions.ErrNotFound
}

func TestEnvironmentPolicyNarrowsTheCap(t *testing.T) {
	open := sandbox.Network{Public: true, Domains: []string{"gitlab.example.com"}}
	closed := sandbox.Network{Public: false, Domains: []string{"gitlab.example.com"}}
	disabled := &v1.EnvironmentNetworkInput{Access: "disabled"}
	restricted := &v1.EnvironmentNetworkInput{Access: "restricted", AllowedDomains: []string{"GitLab.example.com", "pypi.org", "pypi.org"}}

	got, err := egressFor(open, disabled, "https://model.example/v1")
	if want := (sandbox.Egress{PrivateDomains: []string{}, Domains: []string{}, Endpoints: []string{"model.example:443"}}); err != nil || !reflect.DeepEqual(got, want) {
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
