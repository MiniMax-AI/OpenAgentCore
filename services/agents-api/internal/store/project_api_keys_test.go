package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/adminaudit"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func projectKeyDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
func projectKeyPrincipal() identity.Principal {
	return identity.Principal{ProjectScope: identity.ProjectScope{TenantID: uuid.NewString(), OrganizationID: "org-" + uuid.NewString(), ProjectID: "project-" + uuid.NewString()}, SubjectKind: "service_account", SubjectID: "test"}
}
func keyAdminContext(ctx context.Context, id string) context.Context {
	return adminaudit.WithSource(ctx, adminaudit.Source{CredentialID: "12345678", ActorLabel: "test", RequestID: uuid.NewString(), TraceID: uuid.NewString(), TargetKeyID: id})
}
func TestProjectAPIKeyIndependentLifecycle(t *testing.T) {
	s, pool := testStore(t)
	id := uuid.NewString()
	ctx := keyAdminContext(t.Context(), id)
	issued, err := s.CreateProjectAPIKey(ctx, id, " first ")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateProjectAPIKey(keyAdminContext(t.Context(), uuid.NewString()), uuid.NewString(), "second")
	if err != nil {
		t.Fatal(err)
	}
	if issued.TenantID == second.TenantID {
		t.Fatal("keys share tenant")
	}
	asset, err := s.CreateAgent(t.Context(), issued.TenantID, CreateAgentInput{Configuration: []byte(`{"model":"test"}`)})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(issued.Key))
	if err != nil || resolved.Principal.SubjectID != id || resolved.Principal.TenantID != issued.TenantID {
		t.Fatal("incorrect independent principal")
	}
	raw, _ := json.Marshal(resolved.Key)
	if strings.Contains(string(raw), issued.Key) || strings.Contains(string(raw), projectKeyDigest(issued.Key)) {
		t.Fatal("metadata exposed secret")
	}
	var before int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM execution_project_scopes").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProjectAPIKey(keyAdminContext(t.Context(), id), id, "duplicate"); err == nil {
		t.Fatal("duplicate key accepted")
	}
	var after int
	_ = pool.QueryRow(t.Context(), "SELECT count(*) FROM execution_project_scopes").Scan(&after)
	if before != after {
		t.Fatal("duplicate creation leaked tenant")
	}
	requestID := uuid.NewString()
	reset, err := s.ResetProjectAPIKey(keyAdminContext(t.Context(), id), id, requestID)
	if err != nil {
		t.Fatal(err)
	}
	if reset.ID != issued.ID || reset.TenantID != issued.TenantID || reset.Key == issued.Key {
		t.Fatal("reset changed key identity or failed to change secret")
	}
	if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(issued.Key)); !errors.Is(err, ErrNotFound) {
		t.Fatal("old secret remained valid")
	}
	if _, err := s.ResetProjectAPIKey(keyAdminContext(t.Context(), id), id, requestID); !errors.Is(err, ErrProjectAPIKeyExists) {
		t.Fatal("reset retry rotated again")
	}
	if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(reset.Key)); err != nil {
		t.Fatal("reset retry invalidated saved secret")
	}
	if err := s.RevokeProjectAPIKey(keyAdminContext(t.Context(), id), id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(reset.Key)); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked secret authenticated")
	}
	metadata, err := s.GetProjectAPIKey(t.Context(), id)
	if err != nil || metadata.Key.RevokedAt == nil || metadata.Key.TenantID != issued.TenantID {
		t.Fatal("revocation removed space metadata")
	}
	if _, err := s.ResetProjectAPIKey(keyAdminContext(t.Context(), id), id, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked key reset")
	}
	if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(second.Key)); err != nil {
		t.Fatal("revocation affected another key")
	}
	if _, err := s.GetAgent(t.Context(), issued.TenantID, asset.ID); err != nil {
		t.Fatal("revocation lost assets")
	}
	if _, err := s.GetAgent(t.Context(), second.TenantID, asset.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("independent key saw foreign asset")
	}
	if err := s.ValidateProjectKeySeparation(t.Context(), []string{projectKeyDigest(reset.Key)}, nil); err == nil {
		t.Fatal("revoked digest collision accepted")
	}
	if err := s.ValidateProjectKeySeparation(t.Context(), nil, []string{issued.TenantID}); err == nil {
		t.Fatal("issued tenant overlap accepted")
	}
}
func TestProjectAPIKeyAuditFailureRollsBack(t *testing.T) {
	s, _ := testStore(t)
	id := uuid.NewString()
	if _, err := s.CreateProjectAPIKey(t.Context(), id, "unaudited"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("missing audit accepted")
	}
	if _, err := s.GetProjectAPIKey(t.Context(), id); !errors.Is(err, ErrNotFound) {
		t.Fatal("unaudited create persisted")
	}
	issued, err := s.CreateProjectAPIKey(keyAdminContext(t.Context(), id), id, "audited")
	if err != nil {
		t.Fatal(err)
	}
	bad := adminaudit.Source{CredentialID: "", RequestID: uuid.NewString(), TraceID: "trace", TargetKeyID: id}
	ctx := adminaudit.WithSource(t.Context(), bad)
	if _, err := s.ResetProjectAPIKey(ctx, id, uuid.NewString()); err == nil {
		t.Fatal("unaudited reset accepted")
	}
	if err := s.RevokeProjectAPIKey(ctx, id); err == nil {
		t.Fatal("unaudited revoke accepted")
	}
	if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(issued.Key)); err != nil {
		t.Fatal("failed audit changed secret or revocation")
	}
}
func TestProjectAPIKeysValidateBeforeDatabaseAccess(t *testing.T) {
	s := &Store{}
	for _, name := range []string{"", " ", "with\ncontrol", strings.Repeat("x", 81)} {
		if _, err := s.CreateProjectAPIKey(t.Context(), uuid.NewString(), name); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid name accepted")
		}
	}
}
