package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// sandboxProviders interprets the deployment's provider declarations for
// Session admission and placement until they move out of the Store.
var sandboxProviders = providers.Builtin()

// New work and deployment changes share this lock. Existing receipts are checked
// first, preserving idempotent retries and cleanup while maintenance is active.
func checkRuntimeDeploymentAdmission(ctx context.Context, q *sqlc.Queries, installation string) error {
	current, err := q.LockRuntimeDeployment(ctx)
	if err != nil {
		return err
	}
	if !current.InstallationID.Valid {
		return nil
	}
	if current.ResetClear.Valid {
		return ErrSandboxResetAdmission
	}
	if current.AdmissionPaused {
		return fmt.Errorf("%w: sandbox creation is paused for provider maintenance", sessions.ErrEnvironmentUnavailable)
	}
	if current.ProviderKind != "" {
		var spec sandbox.DeploymentSpec
		if json.Unmarshal(current.Specification, &spec) != nil || sandboxProviders.ValidateSpecification(current.ProviderKind, spec) != nil {
			return fmt.Errorf("%w: sandbox creation requires a deployment specification", sessions.ErrEnvironmentUnavailable)
		}
	}
	if installation != "" {
		id, err := parseConnectionGeneration(installation)
		if err != nil || id != current.InstallationID {
			return fmt.Errorf("%w: sandbox installation does not match deployment", sessions.ErrEnvironmentUnavailable)
		}
	}
	return nil
}
