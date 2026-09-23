package store

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/google/uuid"
)

func TestProjectEnvironmentExecutorManagement(t *testing.T) {
	s, _ := testStore(t)
	ctx := t.Context()
	p := FixtureExecutorPrincipal(t, s, uuid.NewString())
	create := func(principal identity.Principal, kind string) (Session, Environment) {
		t.Helper()
		input := environmentInput(uuid.NewString(), kind, "/workspace")
		input.Creator = principal.Subject()
		session, err := s.CreateSession(ctx, principal.TenantID, input)
		if err != nil {
			t.Fatal(err)
		}
		environment, err := s.GetSessionEnvironment(ctx, principal.TenantID, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		return session, environment
	}
	session, one := create(p, "self_hosted")
	_, two := create(p, "self_hosted")
	_, hosted := create(p, "openai_hosted")
	keyID := uuid.NewString()
	issued, err := s.IssueEnvironmentExecutorCredential(ctx, p, one.ID, keyID, false)
	if err != nil || issued.EnvironmentID != one.ID {
		t.Fatal("issue", err)
	}
	// A lost issuance response must not cause a new key or replace the old secret.
	if _, err := s.IssueEnvironmentExecutorCredential(ctx, p, one.ID, keyID, false); !errors.Is(err, ErrExecutorCredentialExists) {
		t.Fatal("uncertain retry", err)
	}
	if _, err := s.AuthenticateEnvironmentExecutor(ctx, one.ID, executorDigest(issued.Token)); err != nil {
		t.Fatal("retry changed credential", err)
	}
	other := p
	other.SubjectID = "different-creator"
	foreign := FixtureExecutorPrincipal(t, s, uuid.NewString())
	for _, principal := range []identity.Principal{other, foreign} {
		if _, err := s.IssueEnvironmentExecutorCredential(ctx, principal, one.ID, uuid.NewString(), false); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign issue", err)
		}
		if _, err := s.IssueEnvironmentExecutorCredential(ctx, principal, one.ID, keyID, true); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign rotate", err)
		}
		if err := s.RevokeEnvironmentExecutorCredential(ctx, principal, one.ID, keyID); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign revoke", err)
		}
	}
	for _, environment := range []string{two.ID, hosted.ID, uuid.NewString()} {
		if _, err := s.IssueEnvironmentExecutorCredential(ctx, p, environment, keyID, true); !errors.Is(err, ErrNotFound) {
			t.Fatal("wrong target rotate", err)
		}
		if err := s.RevokeEnvironmentExecutorCredential(ctx, p, environment, keyID); !errors.Is(err, ErrNotFound) {
			t.Fatal("wrong target revoke", err)
		}
	}
	if _, err := s.IssueEnvironmentExecutorCredential(ctx, p, hosted.ID, uuid.NewString(), false); !errors.Is(err, ErrNotFound) {
		t.Fatal("hosted issuance", err)
	}
	broad, err := s.IssueExecutorCredential(ctx, p, uuid.NewString(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueEnvironmentExecutorCredential(ctx, p, one.ID, broad.KeyID, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("broad rotation", err)
	}
	if err := s.RevokeEnvironmentExecutorCredential(ctx, p, one.ID, broad.KeyID); !errors.Is(err, ErrNotFound) {
		t.Fatal("broad revocation", err)
	}
	rotated, err := s.IssueEnvironmentExecutorCredential(ctx, p, one.ID, keyID, true)
	if err != nil || rotated.Token == issued.Token {
		t.Fatal("rotation", err)
	}
	if _, err := s.AuthenticateEnvironmentExecutor(ctx, one.ID, executorDigest(issued.Token)); !errors.Is(err, ErrNotFound) {
		t.Fatal("old secret", err)
	}
	for range 2 {
		if err := s.RevokeEnvironmentExecutorCredential(ctx, p, one.ID, keyID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AuthenticateEnvironmentExecutor(ctx, one.ID, executorDigest(rotated.Token)); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked secret", err)
	}
	if _, err := s.IssueEnvironmentExecutorCredential(ctx, p, one.ID, keyID, false); !errors.Is(err, ErrExecutorCredentialExists) {
		t.Fatal("resurrected secret", err)
	}
	if err := s.DeleteSession(ctx, p.TenantID, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueEnvironmentExecutorCredential(ctx, p, one.ID, keyID, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted target", err)
	}
}
