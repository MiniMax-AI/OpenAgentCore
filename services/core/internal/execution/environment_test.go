package execution

import (
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

func TestExecutionEnvironmentDoesNotDefaultToLocal(t *testing.T) {
	cases := []struct {
		name     string
		snapshot Snapshot
		none     bool
	}{
		{"public none", Snapshot{Environment: &v1.Environment{Type: "none"}}, true},
		{"missing", Snapshot{}, false},
		{"self-hosted", Snapshot{Environment: &v1.Environment{Type: "self_hosted"}}, false},
		{"hosted", Snapshot{Environment: &v1.Environment{Type: "openai_hosted"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if environmentNone(c.snapshot) != c.none {
				t.Fatal(c.snapshot)
			}
		})
	}
}
