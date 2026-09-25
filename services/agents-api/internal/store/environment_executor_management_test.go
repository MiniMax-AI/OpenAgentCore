package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestProjectEnvironmentExecutorManagement(t *testing.T) {
	s, pool := testStore(t)
	ctx := t.Context()
	project := createTestProject(t, s)
	binding, err := s.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := binding.Principal
	foreignProject := createTestProject(t, s)
	foreign, err := s.GetProject(ctx, foreignProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Each administrator request has its own request ID.
	admin := func() context.Context { return keyAdminContext(ctx, project.ID) }
	create := func(kind string) (Session, Environment) {
		t.Helper()
		input := environmentInput(uuid.NewString(), kind, "/workspace")
		input.Creator = p.Subject()
		session, err := s.CreateSession(ctx, p.TenantID, input)
		if err != nil {
			t.Fatal(err)
		}
		environment, err := s.GetSessionEnvironment(ctx, p.TenantID, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		return session, environment
	}
	session, one := create("self_hosted")
	_, two := create("self_hosted")
	_, hosted := create("openai_hosted")
	keyID := uuid.NewString()

	// The audit entry commits with the write: without an audit source nothing is issued.
	if _, err := s.IssueProjectExecutorCredential(ctx, p, one.ID, keyID, false); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("unaudited issue", err)
	}
	issued, err := s.IssueProjectExecutorCredential(admin(), p, one.ID, keyID, false)
	if err != nil || issued.KeyID != keyID || issued.EnvironmentID != one.ID || issued.Token == "" {
		t.Fatal("issue", err)
	}
	if _, err := s.AuthenticateEnvironmentExecutor(ctx, one.ID, executorDigest(issued.Token)); err != nil {
		t.Fatal("issued credential does not authenticate", err)
	}
	// A lost issuance response must not cause a new key or replace the old secret.
	if _, err := s.IssueProjectExecutorCredential(admin(), p, one.ID, keyID, false); !errors.Is(err, ErrExecutorCredentialExists) {
		t.Fatal("uncertain retry", err)
	}
	listed, err := s.ListProjectExecutorCredentials(ctx, p, one.ID)
	if err != nil || len(listed) != 1 || listed[0].KeyID != keyID || listed[0].CreatedAt.IsZero() || listed[0].RevokedAt != nil {
		t.Fatal("list", listed, err)
	}
	if listed, err := s.ListProjectExecutorCredentials(ctx, p, two.ID); err != nil || len(listed) != 0 {
		t.Fatal("other Environment list", listed, err)
	}

	// Another Project, a hosted or unknown Environment and a key restricted elsewhere are not found.
	if _, err := s.ListProjectExecutorCredentials(ctx, foreign.Principal, one.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign list", err)
	}
	if _, err := s.IssueProjectExecutorCredential(keyAdminContext(ctx, foreignProject.ID), foreign.Principal, one.ID, uuid.NewString(), false); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign issue", err)
	}
	for _, environment := range []string{two.ID, hosted.ID, uuid.NewString()} {
		if _, err := s.IssueProjectExecutorCredential(admin(), p, environment, keyID, true); !errors.Is(err, ErrNotFound) {
			t.Fatal("wrong target rotate", err)
		}
		if err := s.RevokeProjectExecutorCredential(admin(), p, environment, keyID); !errors.Is(err, ErrNotFound) {
			t.Fatal("wrong target revoke", err)
		}
	}
	if _, err := s.IssueProjectExecutorCredential(admin(), p, hosted.ID, uuid.NewString(), false); !errors.Is(err, ErrNotFound) {
		t.Fatal("hosted issuance", err)
	}
	if _, err := s.IssueProjectExecutorCredential(admin(), p, one.ID, uuid.NewString(), true); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown key rotation", err)
	}

	rotated, err := s.IssueProjectExecutorCredential(admin(), p, one.ID, keyID, true)
	if err != nil || rotated.Token == issued.Token {
		t.Fatal("rotation", err)
	}
	if _, err := s.AuthenticateEnvironmentExecutor(ctx, one.ID, executorDigest(issued.Token)); !errors.Is(err, ErrNotFound) {
		t.Fatal("old secret", err)
	}
	for range 2 {
		if err := s.RevokeProjectExecutorCredential(admin(), p, one.ID, keyID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AuthenticateEnvironmentExecutor(ctx, one.ID, executorDigest(rotated.Token)); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked secret", err)
	}
	if listed, err := s.ListProjectExecutorCredentials(ctx, p, one.ID); err != nil || len(listed) != 1 || listed[0].RevokedAt == nil {
		t.Fatal("revoked list", listed, err)
	}
	if _, err := s.IssueProjectExecutorCredential(admin(), p, one.ID, keyID, false); !errors.Is(err, ErrExecutorCredentialExists) {
		t.Fatal("resurrected secret", err)
	}

	var actions []string
	rows, err := pool.Query(ctx, "SELECT action, row_to_json(a)::text FROM admin_audit_log a WHERE resource_type='executor_credential' AND resource_id=$1 ORDER BY created_at", keyID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var action, row string
		if err := rows.Scan(&action, &row); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(row, issued.Token) || strings.Contains(row, rotated.Token) {
			t.Fatal("audit contains a secret")
		}
		actions = append(actions, action)
	}
	if rows.Err() != nil || strings.Join(actions, ",") != "issue,rotate,revoke,revoke" {
		t.Fatal("audit actions", actions, rows.Err())
	}

	if err := s.DeleteSession(ctx, p.TenantID, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListProjectExecutorCredentials(ctx, p, one.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted target list", err)
	}
	if _, err := s.IssueProjectExecutorCredential(admin(), p, one.ID, keyID, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted target rotate", err)
	}
	if err := s.RevokeProjectExecutorCredential(admin(), p, one.ID, keyID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted target revoke", err)
	}
}

// An archived Project gets no new or rotated credential; listing and revoking still work.
func TestArchivedProjectExecutorCredentials(t *testing.T) {
	s, _ := testStore(t)
	ctx := t.Context()
	project := createTestProject(t, s)
	binding, err := s.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := binding.Principal
	input := environmentInput(uuid.NewString(), "self_hosted", "/workspace")
	input.Creator = p.Subject()
	session, err := s.CreateSession(ctx, p.TenantID, input)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(ctx, p.TenantID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	keyID := uuid.NewString()
	if _, err := s.IssueProjectExecutorCredential(keyAdminContext(ctx, project.ID), p, environment.ID, keyID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ArchiveProject(keyAdminContext(ctx, project.ID), project.ID); err != nil {
		t.Fatal(err)
	}
	// Order: the target (404) first, then the archived Project (409), before
	// the key's own exists conflict or unknown-key rotation 404.
	for _, test := range []struct {
		environment, key string
		rotate           bool
		want             error
	}{
		{uuid.NewString(), uuid.NewString(), false, ErrNotFound},
		{environment.ID, uuid.NewString(), false, ErrProjectArchived},
		{environment.ID, keyID, false, ErrProjectArchived},
		{environment.ID, keyID, true, ErrProjectArchived},
		{environment.ID, uuid.NewString(), true, ErrProjectArchived},
	} {
		if _, err := s.IssueProjectExecutorCredential(keyAdminContext(ctx, project.ID), p, test.environment, test.key, test.rotate); !errors.Is(err, test.want) {
			t.Fatal("archived write", test, err)
		}
	}
	if listed, err := s.ListProjectExecutorCredentials(ctx, p, environment.ID); err != nil || len(listed) != 1 {
		t.Fatal("archived list", listed, err)
	}
	if err := s.RevokeProjectExecutorCredential(keyAdminContext(ctx, project.ID), p, environment.ID, keyID); err != nil {
		t.Fatal("archived revoke", err)
	}
	if listed, err := s.ListProjectExecutorCredentials(ctx, p, environment.ID); err != nil || len(listed) != 1 || listed[0].RevokedAt == nil {
		t.Fatal("revoked list", listed, err)
	}
}
