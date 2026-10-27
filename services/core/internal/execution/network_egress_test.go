package execution

import (
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestEgressFollowsTheCapAndTheModelProviderHost(t *testing.T) {
	cap := sandbox.Network{Public: false, Domains: []string{"gitlab.example.com"}}
	for _, tc := range []struct {
		baseURL string
		want    sandbox.Egress
	}{
		{"https://API.Model.example:8443/v1", sandbox.Egress{PrivateDomains: []string{"gitlab.example.com"}, Endpoints: []string{"api.model.example"}}},
		{"http://10.1.2.3:8000/v1", sandbox.Egress{PrivateDomains: []string{"gitlab.example.com"}, Endpoints: []string{"10.1.2.3"}}},
		{"http://127.0.0.1:8000/v1", sandbox.Egress{PrivateDomains: []string{"gitlab.example.com"}, Endpoints: []string{}}},
		{"http://localhost:8000", sandbox.Egress{PrivateDomains: []string{"gitlab.example.com"}, Endpoints: []string{}}},
		{"", sandbox.Egress{PrivateDomains: []string{"gitlab.example.com"}, Endpoints: []string{}}},
	} {
		if got := egressFor(cap, tc.baseURL); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%q: %+v, want %+v", tc.baseURL, got, tc.want)
		}
	}
	if got := egressFor(sandbox.DefaultNetwork(), ""); !got.Public || len(got.PrivateDomains) != 0 {
		t.Fatalf("default cap: %+v", got)
	}
}
