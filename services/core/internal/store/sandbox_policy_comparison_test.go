package store

import (
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestSandboxSelectionComparesRegisteredPolicyForNodeProviders(t *testing.T) {
	for _, kind := range []string{"docker", "microsandbox"} {
		t.Run(kind, func(t *testing.T) {
			id := uuid.New()
			policy, err := providers.Describe(kind, id.String())
			if err != nil {
				t.Fatal(err)
			}
			input := SandboxDeploymentSetupRequest{Provider: kind, DeploymentSpec: SandboxDeploymentTestSpec(kind)}
			spec, err := json.Marshal(input.DeploymentSpec)
			if err != nil {
				t.Fatal(err)
			}
			original := sqlc.RuntimeDeployment{WebManaged: true, ProviderKind: kind, InstallationID: pgtype.UUID{Bytes: id, Valid: true}, Specification: spec, IdleSeconds: policy.IdleSeconds, RetentionSeconds: policy.RetentionSeconds}
			for _, tc := range []struct {
				name            string
				idle, retention int64
				equal           bool
			}{
				{"unchanged", policy.IdleSeconds, policy.RetentionSeconds, true},
				{"idle_policy_changed", policy.IdleSeconds + 1, policy.RetentionSeconds, false},
				{"retention_policy_changed", policy.IdleSeconds, policy.RetentionSeconds + 1, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					d := original
					d.IdleSeconds, d.RetentionSeconds = tc.idle, tc.retention
					got, err := (&Store{}).sandboxSelectionEqual(d, input)
					if err != nil || got != tc.equal {
						t.Fatalf("selection equality = %v, %v; want %v", got, err, tc.equal)
					}
					if d.IdleSeconds != tc.idle || d.RetentionSeconds != tc.retention {
						t.Fatal("comparison mutated stored policy")
					}
				})
			}
		})
	}
}
