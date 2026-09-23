package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/google/uuid"
)

func projectKeyDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func projectKeyPrincipal() identity.Principal {
	return identity.Principal{ProjectScope: identity.ProjectScope{
		TenantID: uuid.NewString(), OrganizationID: "org-" + uuid.NewString(), ProjectID: "project-" + uuid.NewString(),
	}, SubjectKind: "service_account", SubjectID: "console-admin"}
}

func TestProjectAPIKeysPersistWithoutSecretsAndRevoke(t *testing.T) {
	s, pool := testStore(t)
	ctx := t.Context()
	owner := projectKeyPrincipal()
	binding := projectKeyDigest(uuid.NewString())
	id := uuid.NewString()
	issued, err := s.CreateProjectAPIKey(ctx, binding, owner, id, "  Local terminal 云  ")
	if err != nil {
		t.Fatal(err)
	}
	random, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(issued.Key, "pc_"))
	if err != nil || len(random) != 32 || !strings.HasPrefix(issued.Key, "pc_") || issued.Prefix != issued.Key[:11] ||
		issued.ID != id || issued.Name != "Local terminal 云" || issued.CreatedAt.IsZero() || issued.RevokedAt != nil {
		t.Fatal("issued key does not satisfy its one-time secret and metadata contract")
	}
	if duplicate, err := s.CreateProjectAPIKey(ctx, binding, owner, id, "replacement"); !errors.Is(err, ErrProjectAPIKeyExists) || duplicate.Key != "" {
		t.Fatalf("duplicate ID must not replace or recover a secret: %v", err)
	}
	// New Store and pool instances must recover metadata and authentication, not plaintext.
	pool.Close()
	restarted, _ := testStore(t)
	resolved, err := restarted.ResolveProjectAPIKey(ctx, projectKeyDigest(issued.Key))
	if err != nil || resolved.BindingDigest != binding || resolved.Principal != owner {
		t.Fatalf("frozen binding did not survive restart: %v", err)
	}
	listed, err := restarted.ListProjectAPIKeys(ctx, binding, owner)
	if err != nil || len(listed) != 1 || !reflect.DeepEqual(listed[0], issued.ProjectAPIKey) {
		t.Fatalf("persisted safe metadata changed: %v", err)
	}
	raw, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{issued.Key, projectKeyDigest(issued.Key), binding, "token_sha256", "binding_digest", `"key"`} {
		if strings.Contains(string(raw), private) {
			t.Fatal("list returned private key material")
		}
	}
	second, err := restarted.CreateProjectAPIKey(ctx, binding, owner, uuid.NewString(), issued.Name)
	if err != nil || second.Key == issued.Key {
		t.Fatalf("same name must allow an independent key: %v", err)
	}
	for _, unknown := range []string{"", "bad-digest", strings.Repeat("A", 64), projectKeyDigest("unknown")} {
		if _, err := restarted.ResolveProjectAPIKey(ctx, unknown); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown/malformed digest authenticated: %v", err)
		}
	}
	if err := restarted.RevokeProjectAPIKey(ctx, binding, owner, id); err != nil {
		t.Fatal(err)
	}
	listed, err = restarted.ListProjectAPIKeys(ctx, binding, owner)
	if err != nil || len(listed) != 2 {
		t.Fatalf("list after revoke: %v", err)
	}
	var revoked ProjectAPIKey
	for _, key := range listed {
		if key.ID == id {
			revoked = key
		}
	}
	if revoked.RevokedAt == nil {
		t.Fatal("revocation metadata missing")
	}
	if err := restarted.RevokeProjectAPIKey(ctx, binding, owner, id); err != nil {
		t.Fatalf("repeat revoke must be idempotent: %v", err)
	}
	if _, err := restarted.ResolveProjectAPIKey(ctx, projectKeyDigest(issued.Key)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked key authenticated: %v", err)
	}
	if _, err := restarted.ResolveProjectAPIKey(ctx, projectKeyDigest(second.Key)); err != nil {
		t.Fatalf("revocation affected independent key: %v", err)
	}
	listed, err = restarted.ListProjectAPIKeys(ctx, binding, owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range listed {
		if key.ID == id && !reflect.DeepEqual(key, revoked) {
			t.Fatal("idempotent revoke changed its original timestamp")
		}
	}
}

func TestProjectAPIKeysRequireFullPrincipalAndParentForManagement(t *testing.T) {
	s, _ := testStore(t)
	owner := projectKeyPrincipal()
	binding := projectKeyDigest(uuid.NewString())
	issued, err := s.CreateProjectAPIKey(t.Context(), binding, owner, uuid.NewString(), "owned")
	if err != nil {
		t.Fatal(err)
	}
	type caller struct {
		binding string
		owner   identity.Principal
	}
	callers := []caller{{projectKeyDigest("other parent"), owner}, {binding, projectKeyPrincipal()}}
	for _, field := range []string{"tenant", "organization", "project", "subject_kind", "subject_id"} {
		other := owner
		switch field {
		case "tenant":
			other.TenantID = uuid.NewString()
		case "organization":
			other.OrganizationID += "-other"
		case "project":
			other.ProjectID += "-other"
		case "subject_kind":
			other.SubjectKind = "user"
		case "subject_id":
			other.SubjectID += "-other"
		}
		callers = append(callers, caller{binding, other})
	}
	for i, other := range callers {
		keys, err := s.ListProjectAPIKeys(t.Context(), other.binding, other.owner)
		if err != nil || len(keys) != 0 {
			t.Fatalf("foreign caller %d listed a key: %v", i, err)
		}
		if err := s.RevokeProjectAPIKey(t.Context(), other.binding, other.owner, issued.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign caller %d revoked a key: %v", i, err)
		}
	}
	if err := s.RevokeProjectAPIKey(t.Context(), binding, owner, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown ID revoke: %v", err)
	}
	if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(issued.Key)); err != nil {
		t.Fatalf("foreign management changed the key: %v", err)
	}
	otherParent, err := s.CreateProjectAPIKey(t.Context(), projectKeyDigest("other parent"), owner, uuid.NewString(), "owned")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := s.ListProjectAPIKeys(t.Context(), projectKeyDigest("other parent"), owner)
	if err != nil || len(keys) != 1 || keys[0].ID != otherParent.ID {
		t.Fatalf("parent key ownership mixed: %v", err)
	}
}

func TestProjectAPIKeysValidateBeforeDatabaseAccess(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	owner := projectKeyPrincipal()
	binding := projectKeyDigest("parent")
	for _, name := range []string{"", "  ", strings.Repeat("云", 81), "with\ncontrol", "\ttrimmed", "nul\x00", string([]byte{0xff})} {
		if _, err := s.CreateProjectAPIKey(ctx, binding, owner, uuid.NewString(), name); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid name accepted: %v", err)
		}
	}
	for _, digest := range []string{"", "abc", strings.Repeat("A", 64), strings.Repeat("z", 64)} {
		if _, err := s.CreateProjectAPIKey(ctx, digest, owner, uuid.NewString(), "name"); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid binding accepted: %v", err)
		}
		if _, err := s.ListProjectAPIKeys(ctx, digest, owner); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid list binding accepted: %v", err)
		}
	}
	for _, id := range []string{"", "invalid", uuid.Nil.String()} {
		if _, err := s.CreateProjectAPIKey(ctx, binding, owner, id, "name"); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid create ID accepted: %v", err)
		}
		if err := s.RevokeProjectAPIKey(ctx, binding, owner, id); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid revoke ID accepted: %v", err)
		}
	}
	owner.SubjectKind = "unknown"
	if _, err := s.CreateProjectAPIKey(ctx, binding, owner, uuid.NewString(), "name"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid principal accepted: %v", err)
	}
}
