package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func TestProjectEnvironmentExecutorManagement(t *testing.T) {
	s, pool := testStore(t)
	ctx := t.Context()
	binding := createTestProject(t, pool)
	project, p := binding.Project, binding.Principal
	foreign := createTestProject(t, pool)
	foreignProject := foreign.Project
	// Each administrator request has its own request ID.
	admin := func() context.Context { return keyAdminContext(ctx, project.ID) }
	create := func(kind string) (sessions.Session, sessions.Environment) {
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
	if _, err := s.IssueProjectExecutorCredential(ctx, p, one.ID, keyID, false); !errors.Is(err, adminaudit.ErrInvalidSource) {
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
	if _, err := s.IssueProjectExecutorCredential(admin(), p, one.ID, keyID, false); !errors.Is(err, sessions.ErrExecutorCredentialExists) {
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
	if _, err := s.ListProjectExecutorCredentials(ctx, foreign.Principal, one.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("foreign list", err)
	}
	if _, err := s.IssueProjectExecutorCredential(keyAdminContext(ctx, foreignProject.ID), foreign.Principal, one.ID, uuid.NewString(), false); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("foreign issue", err)
	}
	for _, environment := range []string{two.ID, hosted.ID, uuid.NewString()} {
		if _, err := s.IssueProjectExecutorCredential(admin(), p, environment, keyID, true); !errors.Is(err, sessions.ErrNotFound) {
			t.Fatal("wrong target rotate", err)
		}
		if err := s.RevokeProjectExecutorCredential(admin(), p, environment, keyID); !errors.Is(err, sessions.ErrNotFound) {
			t.Fatal("wrong target revoke", err)
		}
	}
	if _, err := s.IssueProjectExecutorCredential(admin(), p, hosted.ID, uuid.NewString(), false); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("hosted issuance", err)
	}
	if _, err := s.IssueProjectExecutorCredential(admin(), p, one.ID, uuid.NewString(), true); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("unknown key rotation", err)
	}

	rotated, err := s.IssueProjectExecutorCredential(admin(), p, one.ID, keyID, true)
	if err != nil || rotated.Token == issued.Token {
		t.Fatal("rotation", err)
	}
	if _, err := s.AuthenticateEnvironmentExecutor(ctx, one.ID, executorDigest(issued.Token)); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("old secret", err)
	}
	for range 2 {
		if err := s.RevokeProjectExecutorCredential(admin(), p, one.ID, keyID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AuthenticateEnvironmentExecutor(ctx, one.ID, executorDigest(rotated.Token)); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("revoked secret", err)
	}
	if listed, err := s.ListProjectExecutorCredentials(ctx, p, one.ID); err != nil || len(listed) != 1 || listed[0].RevokedAt == nil {
		t.Fatal("revoked list", listed, err)
	}
	if _, err := s.IssueProjectExecutorCredential(admin(), p, one.ID, keyID, false); !errors.Is(err, sessions.ErrExecutorCredentialExists) {
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
	if _, err := s.ListProjectExecutorCredentials(ctx, p, one.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("deleted target list", err)
	}
	if _, err := s.IssueProjectExecutorCredential(admin(), p, one.ID, keyID, true); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("deleted target rotate", err)
	}
	if err := s.RevokeProjectExecutorCredential(admin(), p, one.ID, keyID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("deleted target revoke", err)
	}
}

// An archived Project gets no new or rotated credential; listing and revoking still work.
func TestArchivedProjectExecutorCredentials(t *testing.T) {
	s, pool := testStore(t)
	ctx := t.Context()
	binding := createTestProject(t, pool)
	project, p := binding.Project, binding.Principal
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
	if _, err := testProjects(t, pool).ArchiveProject(keyAdminContext(ctx, project.ID), projects.ArchiveProject{ID: project.ID}); err != nil {
		t.Fatal(err)
	}
	// Order: the target (404) first, then the archived Project (409), before
	// the key's own exists conflict or unknown-key rotation 404.
	for _, test := range []struct {
		environment, key string
		rotate           bool
		want             error
	}{
		{uuid.NewString(), uuid.NewString(), false, sessions.ErrNotFound},
		{environment.ID, uuid.NewString(), false, projects.ErrArchived},
		{environment.ID, keyID, false, projects.ErrArchived},
		{environment.ID, keyID, true, projects.ErrArchived},
		{environment.ID, uuid.NewString(), true, projects.ErrArchived},
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

func TestProjectExecutorConnectionState(t *testing.T) {
	s, pool := testStore(t)
	ctx := t.Context()
	binding := createTestProject(t, pool)
	project := binding.Project
	session, env, key := runtimeEnrollmentFixture(t, s, binding.Principal)
	state, err := s.ProjectExecutorCredentialState(ctx, binding.Principal, env.ID)
	if err != nil || state.Connection.DeviceID != "" || state.Connection.EnrolledAt != nil || state.Connection.BoundKeyID != nil || state.Connection.LastSeenAt != nil {
		t.Fatal("never enrolled", state, err)
	}
	enrolled, err := s.EnrollRuntime(ctx, env.ID, executorDigest(key.Token))
	if err != nil {
		t.Fatal(err)
	}
	check := func() sessions.ExecutorCredentialState {
		t.Helper()
		v, e := s.ProjectExecutorCredentialState(ctx, binding.Principal, env.ID)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	state = check()
	if state.Connection.DeviceID != enrolled.DeviceID || state.Connection.BoundKeyID == nil || *state.Connection.BoundKeyID != key.KeyID || state.Connection.EnrolledAt == nil || state.Connection.LastSeenAt != nil || state.Connection.CredentialHash != executorDigest(key.Token) {
		t.Fatal("binding", state)
	}
	for _, spelling := range []string{env.ID, strings.ToUpper(env.ID), strings.ReplaceAll(env.ID, "-", "")} {
		resolved, err := s.ProjectExecutorCredentialState(ctx, binding.Principal, spelling)
		if err != nil || resolved.EnvironmentID != env.ID || resolved.Connection.DeviceID != enrolled.DeviceID || resolved.Connection.CredentialHash != executorDigest(key.Token) {
			t.Fatal("equivalent target did not retain canonical identity and binding", spelling, resolved, err)
		}
	}
	// An additional credential never changes the enrolled device's bound key.
	if _, err = s.IssueProjectExecutorCredential(keyAdminContext(ctx, project.ID), binding.Principal, env.ID, uuid.NewString(), false); err != nil {
		t.Fatal(err)
	}
	state = check()
	if *state.Connection.BoundKeyID != key.KeyID || len(state.Credentials) != 2 {
		t.Fatal("second key changed binding")
	}
	if _, err = pool.Exec(ctx, "UPDATE devices SET last_seen_at=clock_timestamp() WHERE id=$1", enrolled.DeviceID); err != nil {
		t.Fatal(err)
	}
	state = check()
	if state.Connection.LastSeenAt == nil {
		t.Fatal("heartbeat history missing")
	}
	rotated, err := s.IssueProjectExecutorCredential(keyAdminContext(ctx, project.ID), binding.Principal, env.ID, key.KeyID, true)
	if err != nil {
		t.Fatal(err)
	}
	state = check()
	if state.Connection.CredentialHash != executorDigest(rotated.Token) || *state.Connection.BoundKeyID != key.KeyID {
		t.Fatal("rotation must expose only the fresh authority internally")
	}
	if _, err = pool.Exec(ctx, "UPDATE environments SET status='expired' WHERE id=$1", env.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateEnvironmentExecutor(ctx, env.ID, executorDigest(rotated.Token)); err != nil {
		t.Fatal("fixture executor key should still authenticate", err)
	}
	state = check()
	if state.Connection.CredentialHash != "" {
		t.Fatal("retired Environment retained device authority")
	}
	if _, err = pool.Exec(ctx, "UPDATE environments SET status='connected' WHERE id=$1", env.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeProjectExecutorCredential(keyAdminContext(ctx, project.ID), binding.Principal, env.ID, key.KeyID); err != nil {
		t.Fatal(err)
	}
	state = check()
	if state.Connection.CredentialHash != "" || state.Connection.EnrolledAt == nil || *state.Connection.BoundKeyID != key.KeyID {
		t.Fatal("revocation lost history or retained authority")
	}
	raw, err := json.Marshal(state.Connection)
	if err != nil || string(raw) != "{}" {
		t.Fatal("internal facts serialize", string(raw), err)
	}
	// Existing target visibility is preserved: an expired self-hosted Environment
	// is readable but never has runtime_device_authority; deletion removes it.
	if _, err = pool.Exec(ctx, "UPDATE environments SET status='expired' WHERE id=$1", env.ID); err != nil {
		t.Fatal(err)
	}
	state = check()
	if state.Connection.EnvironmentStatus != "expired" || state.Connection.CredentialHash != "" {
		t.Fatal("expired authority")
	}
	foreign := createTestProject(t, pool)
	for _, id := range []string{uuid.NewString(), "malformed"} {
		if _, err = s.ProjectExecutorCredentialState(ctx, binding.Principal, id); !errors.Is(err, sessions.ErrNotFound) {
			t.Fatal("missing target", err)
		}
	}
	if _, err = s.ProjectExecutorCredentialState(ctx, foreign.Principal, env.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("foreign target", err)
	}
	if _, err = pool.Exec(ctx, "UPDATE sessions SET deleted_at=clock_timestamp() WHERE id=$1", session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProjectExecutorCredentialState(ctx, binding.Principal, env.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("deleted target", err)
	}
}
