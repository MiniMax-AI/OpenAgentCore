package sessionpg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestInstallationSignaturesUseTheCredentialKey(t *testing.T) {
	keyed := New(nil, frozenCipher(t))
	signature, err := keyed.SignInstallation(t.Context(), "payload")
	if err != nil {
		t.Fatal(err)
	}
	if err := keyed.VerifyInstallation(t.Context(), "payload", signature); err != nil {
		t.Fatal(err)
	}
	if err := keyed.VerifyInstallation(t.Context(), "other", signature); !errors.Is(err, sessions.ErrInstallationAuthorization) {
		t.Fatalf("another payload: %v", err)
	}
	keyless := New(nil, nil)
	if _, err := keyless.SignInstallation(t.Context(), "payload"); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatalf("keyless signature: %v", err)
	}
	if err := keyless.VerifyInstallation(t.Context(), "payload", signature); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatalf("keyless verification: %v", err)
	}
}

func TestExecutorCredentialTxTranslatesOutcomes(t *testing.T) {
	pool := pgtest.Open(t)
	store := New(pgunit.NewPool(pool), nil)
	tenantID, sessionID, environmentID := newEnvironment(t, pool, "self_hosted", "pending")
	tenant, environment := uuidText(tenantID), uuidText(environmentID)
	exec(t, pool, `INSERT INTO execution_project_scopes(tenant_id, organization_id, project_id) VALUES ($1, 'org', $2)`, tenantID, tenant)
	principal := identity.Principal{ProjectScope: identity.ProjectScope{TenantID: tenant, OrganizationID: "org", ProjectID: tenant}, SubjectKind: "user", SubjectID: "owner"}
	within := func(apply func(context.Context, sessions.EnvironmentExecutorCredentialTx) error) {
		t.Helper()
		err := store.WithEnvironmentExecutorCredentials(t.Context(), tenant, environment, func(ctx context.Context, tx sessions.EnvironmentExecutorCredentialTx, _ sessions.LockedSession) error {
			return apply(ctx, tx)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	within(func(ctx context.Context, tx sessions.EnvironmentExecutorCredentialTx) error {
		if _, found, err := tx.LoadSessionCreator(ctx); err != nil || found {
			return fmt.Errorf("creator of a Session without one: %v %v", found, err)
		}
		return nil
	})
	exec(t, pool, `UPDATE sessions SET creator_kind = 'user', creator_id = 'owner' WHERE id = $1`, sessionID)
	// Digests are unique across tenants, so each run presents its own.
	key, digest := uuid.NewString(), strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")
	other := identity.Subject{Kind: "user", ID: "other"}
	within(func(ctx context.Context, tx sessions.EnvironmentExecutorCredentialTx) error {
		if creator, found, err := tx.LoadSessionCreator(ctx); err != nil || !found || creator != principal.Subject() {
			return fmt.Errorf("creator %+v %v %v", creator, found, err)
		}
		if _, err := tx.LockProject(ctx); !errors.Is(err, sessions.ErrNotFound) {
			return fmt.Errorf("lock of a missing Project: %v", err)
		}
		grant := sessions.ExecutorCredentialGrant{Principal: principal, KeyID: key, EnvironmentID: environment, Digest: digest}
		if issued, err := tx.IssueExecutorCredential(ctx, grant); err != nil || issued != (sessions.IssuedExecutorCredential{KeyID: key, EnvironmentID: environment}) {
			return fmt.Errorf("issue %+v %v", issued, err)
		}
		if _, err := tx.IssueExecutorCredential(ctx, grant); !errors.Is(err, sessions.ErrExecutorCredentialExists) {
			return fmt.Errorf("reissue: %v", err)
		}
		for presented, want := range map[string]bool{digest: true, strings.Repeat("b", 64): false} {
			if current, err := tx.AuthenticateExecutor(ctx, environment, presented); err != nil || current != want {
				return fmt.Errorf("authenticate %s: %v %v", presented, current, err)
			}
		}
		if _, err := tx.RotateExecutorCredential(ctx, other, key, digest); !errors.Is(err, sessions.ErrNotFound) {
			return fmt.Errorf("another subject's rotation: %v", err)
		}
		if err := tx.RevokeExecutorCredential(ctx, other, key); !errors.Is(err, sessions.ErrNotFound) {
			return fmt.Errorf("another subject's revocation: %v", err)
		}
		return nil
	})
}
