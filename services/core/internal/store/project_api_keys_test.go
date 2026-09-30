package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/google/uuid"
)

func projectKeyDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
func projectKeyPrincipal() identity.Principal {
	return identity.Principal{ProjectScope: identity.ProjectScope{TenantID: uuid.NewString(), OrganizationID: "org-" + uuid.NewString(), ProjectID: "project-" + uuid.NewString()}, SubjectKind: "service_account", SubjectID: "test"}
}
func keyAdminContext(ctx context.Context, id string) context.Context {
	return adminaudit.WithSource(ctx, adminaudit.Source{CredentialID: "12345678", ActorLabel: "test", RequestID: uuid.NewString(), TraceID: uuid.NewString(), ProjectID: id})
}
func createTestProject(t *testing.T, s *Store) Project {
	t.Helper()
	id := uuid.NewString()
	p, err := s.CreateProject(keyAdminContext(t.Context(), id), id, "Project")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestProjectKeysShareIdentityAndArchiveRetainsAssets(t *testing.T) {
	s, _ := testStore(t)
	p := createTestProject(t, s)
	other := createTestProject(t, s)
	first, err := s.CreateProjectAPIKey(keyAdminContext(t.Context(), p.ID), p.ID, uuid.NewString(), "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateProjectAPIKey(keyAdminContext(t.Context(), p.ID), p.ID, uuid.NewString(), "second")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(first.Key))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(second.Key))
	if err != nil || a.Principal != b.Principal || a.Principal.SubjectID != "project:"+p.ID || a.Principal.ProjectID != "proj_"+p.ID {
		t.Fatal("Project keys do not share the stable Project principal", err)
	}
	asset, err := s.CreateAgent(t.Context(), a.Principal.TenantID, CreateAgentInput{Configuration: []byte(`{"model":"test"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAgent(t.Context(), b.Principal.TenantID, asset.ID); err != nil {
		t.Fatal("peer key cannot read shared asset", err)
	}
	if _, err := s.GetAgent(t.Context(), other.TenantID, asset.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign Project read asset", err)
	}
	renamed, err := s.RenameProject(keyAdminContext(t.Context(), p.ID), p.ID, "renamed")
	if err != nil || renamed.TenantID != p.TenantID || renamed.ActiveKeyCount != 2 {
		t.Fatal("rename changed Project ownership", err)
	}
	afterRename, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(first.Key))
	if err != nil || afterRename.Principal != a.Principal {
		t.Fatal("rename changed key principal", err)
	}
	listed, err := s.ListProjectAPIKeys(t.Context(), p.ID, "", 50, true)
	if err != nil || len(listed.Data) != 2 {
		t.Fatal("Project keys missing", err)
	}
	raw, _ := json.Marshal(listed)
	if strings.Contains(string(raw), first.Key) || strings.Contains(string(raw), projectKeyDigest(first.Key)) {
		t.Fatal("key list exposed credential")
	}
	if err := s.RevokeProjectAPIKey(keyAdminContext(t.Context(), other.ID), other.ID, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign Project revoked key", err)
	}
	if err := s.RevokeProjectAPIKey(keyAdminContext(t.Context(), p.ID), p.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(first.Key)); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked key authenticated")
	}
	if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(second.Key)); err != nil {
		t.Fatal("revocation affected peer", err)
	}
	archived, err := s.ArchiveProject(keyAdminContext(t.Context(), p.ID), p.ID)
	if err != nil || archived.ArchivedAt == nil || archived.ActiveKeyCount != 0 {
		t.Fatal("archive did not revoke all keys", err)
	}
	if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(second.Key)); !errors.Is(err, ErrNotFound) {
		t.Fatal("archived Project authenticated")
	}
	if _, err := s.CreateProjectAPIKey(keyAdminContext(t.Context(), p.ID), p.ID, uuid.NewString(), "late"); !errors.Is(err, ErrProjectArchived) {
		t.Fatal("archived Project admitted new key", err)
	}
	if _, err := s.GetAgent(t.Context(), p.TenantID, asset.ID); err != nil {
		t.Fatal("archive removed assets", err)
	}
	if err := s.ValidateProjectKeySeparation(t.Context(), []string{projectKeyDigest(second.Key)}); err == nil {
		t.Fatal("revoked credential collision accepted")
	}
	raw, _ = json.Marshal(archived)
	if strings.Contains(string(raw), p.TenantID) {
		t.Fatal("Project response exposed internal tenant")
	}
}
func TestProjectManagementRequiresAtomicAudit(t *testing.T) {
	s, _ := testStore(t)
	id := uuid.NewString()
	if _, err := s.CreateProject(t.Context(), id, "unaudited"); !errors.Is(err, adminaudit.ErrInvalidSource) {
		t.Fatal("unaudited Project accepted")
	}
	if _, err := s.GetProject(t.Context(), id); !errors.Is(err, ErrNotFound) {
		t.Fatal("unaudited Project persisted")
	}
	p := createTestProject(t, s)
	keyID := uuid.NewString()
	if _, err := s.CreateProjectAPIKey(t.Context(), p.ID, keyID, "unaudited"); !errors.Is(err, adminaudit.ErrInvalidSource) {
		t.Fatal("unaudited key accepted")
	}
	page, err := s.ListProjectAPIKeys(t.Context(), p.ID, "", 20, true)
	if err != nil || len(page.Data) != 0 {
		t.Fatal("unaudited key persisted")
	}
	if _, err := s.RenameProject(t.Context(), p.ID, "bad"); !errors.Is(err, adminaudit.ErrInvalidSource) {
		t.Fatal("unaudited rename accepted")
	}
	if _, err := s.ArchiveProject(t.Context(), p.ID); !errors.Is(err, adminaudit.ErrInvalidSource) {
		t.Fatal("unaudited archive accepted")
	}
	current, err := s.GetProject(t.Context(), p.ID)
	if err != nil || current.Project.Name != p.Name || current.Project.ArchivedAt != nil {
		t.Fatal("unaudited mutation persisted")
	}
}
func TestProjectNamesValidateBeforeDatabaseAccess(t *testing.T) {
	s := &Store{}
	for _, name := range []string{"", " ", "with\ncontrol", strings.Repeat("x", 129)} {
		if _, err := s.CreateProject(t.Context(), uuid.NewString(), name); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid Project name accepted")
		}
	}
	for _, name := range []string{"", " ", "with\ncontrol", strings.Repeat("x", 81)} {
		if _, err := s.CreateProjectAPIKey(t.Context(), uuid.NewString(), uuid.NewString(), name); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid key name accepted")
		}
	}
}

func TestProjectCatalogPaginationAndScopedKeyCursor(t *testing.T) {
	s, _ := testStore(t)
	first, second := createTestProject(t, s), createTestProject(t, s)
	if _, err := s.CreateProject(keyAdminContext(t.Context(), first.ID), first.ID, "duplicate"); !errors.Is(err, ErrProjectExists) {
		t.Fatal("duplicate Project ID did not conflict", err)
	}
	// Equal display names do not merge Projects or keys.
	a, err := s.CreateProjectAPIKey(keyAdminContext(t.Context(), first.ID), first.ID, uuid.NewString(), "same name")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateProjectAPIKey(keyAdminContext(t.Context(), first.ID), first.ID, uuid.NewString(), "same name")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProjectAPIKey(keyAdminContext(t.Context(), first.ID), first.ID, a.ID, "duplicate ID"); !errors.Is(err, ErrProjectAPIKeyExists) {
		t.Fatal("duplicate key ID did not conflict", err)
	}
	page, err := s.ListProjectAPIKeys(t.Context(), first.ID, "", 1, true)
	if err != nil || len(page.Data) != 1 || !page.HasMore {
		t.Fatal("first key page invalid", err)
	}
	tail, err := s.ListProjectAPIKeys(t.Context(), first.ID, page.Data[0].ID, 1, true)
	if err != nil || len(tail.Data) != 1 || tail.HasMore || tail.Data[0].ID <= page.Data[0].ID {
		t.Fatal("key cursor did not advance", err)
	}
	reverse, err := s.ListProjectAPIKeys(t.Context(), first.ID, "", 1, false)
	if err != nil || len(reverse.Data) != 1 || reverse.Data[0].ID != tail.Data[0].ID {
		t.Fatal("descending key page invalid", err)
	}
	if _, err := s.ListProjectAPIKeys(t.Context(), second.ID, a.ID, 1, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign key cursor accepted", err)
	}
	if _, err := s.ListProjectAPIKeys(t.Context(), first.ID, "", 101, true); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("oversized key page accepted", err)
	}
	if _, err := s.ListProjects(t.Context(), "", 101, true); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("oversized Project page accepted", err)
	}
	if err := s.RevokeProjectAPIKey(keyAdminContext(t.Context(), first.ID), first.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeProjectAPIKey(keyAdminContext(t.Context(), first.ID), first.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetProject(t.Context(), first.ID)
	if err != nil || current.Project.ArchivedAt != nil || current.Project.ActiveKeyCount != 0 {
		t.Fatal("last key removal changed Project lifecycle", err)
	}
	if _, err := s.CreateProjectAPIKey(keyAdminContext(t.Context(), first.ID), first.ID, uuid.NewString(), "new access"); err != nil {
		t.Fatal("zero-key Project could not issue another key", err)
	}
}
