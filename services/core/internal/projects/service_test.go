package projects

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// fakeStorage fails the test on any call whose func is unset.
type fakeStorage struct {
	t               testing.TB
	createProject   func(context.Context, NewProject) (Project, error)
	renameProject   func(context.Context, string, string) (Project, error)
	archiveProject  func(context.Context, string) (Project, error)
	withKeyIssuance func(context.Context, string, func(KeyIssuanceTx) error) error
	revokeAPIKey    func(context.Context, string, string) error
}

func (f *fakeStorage) CreateProject(ctx context.Context, project NewProject) (Project, error) {
	if f.createProject == nil {
		f.t.Fatal("unexpected call to CreateProject")
	}
	return f.createProject(ctx, project)
}
func (f *fakeStorage) RenameProject(ctx context.Context, id, name string) (Project, error) {
	if f.renameProject == nil {
		f.t.Fatal("unexpected call to RenameProject")
	}
	return f.renameProject(ctx, id, name)
}
func (f *fakeStorage) ArchiveProject(ctx context.Context, id string) (Project, error) {
	if f.archiveProject == nil {
		f.t.Fatal("unexpected call to ArchiveProject")
	}
	return f.archiveProject(ctx, id)
}
func (f *fakeStorage) WithKeyIssuance(ctx context.Context, projectID string, issue func(KeyIssuanceTx) error) error {
	if f.withKeyIssuance == nil {
		f.t.Fatal("unexpected call to WithKeyIssuance")
	}
	return f.withKeyIssuance(ctx, projectID, issue)
}
func (f *fakeStorage) RevokeAPIKey(ctx context.Context, projectID, keyID string) error {
	if f.revokeAPIKey == nil {
		f.t.Fatal("unexpected call to RevokeAPIKey")
	}
	return f.revokeAPIKey(ctx, projectID, keyID)
}

type fakeKeyIssuance struct {
	t           testing.TB
	loadProject func(context.Context) (LockedProject, error)
	applyAPIKey func(context.Context, NewAPIKey) (APIKey, error)
}

func (f *fakeKeyIssuance) LoadProject(ctx context.Context) (LockedProject, error) {
	if f.loadProject == nil {
		f.t.Fatal("unexpected call to LoadProject")
	}
	return f.loadProject(ctx)
}
func (f *fakeKeyIssuance) ApplyAPIKey(ctx context.Context, key NewAPIKey) (APIKey, error) {
	if f.applyAPIKey == nil {
		f.t.Fatal("unexpected call to ApplyAPIKey")
	}
	return f.applyAPIKey(ctx, key)
}

func newTestService(t *testing.T, storage *fakeStorage) *Service {
	t.Helper()
	storage.t = t
	s, err := NewService(storage)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNewServiceRequiresStorage(t *testing.T) {
	if _, err := NewService(nil); err == nil {
		t.Fatal("nil storage accepted")
	}
}

func TestNamesValidateBeforeStorage(t *testing.T) {
	s := newTestService(t, &fakeStorage{})
	for _, name := range []string{"", " ", "with\ncontrol", strings.Repeat("x", ProjectNameMaxLength+1)} {
		var field *NameError
		if _, err := s.CreateProject(t.Context(), CreateProject{ID: "11111111-1111-4111-8111-111111111111", Name: name}); !errors.As(err, &field) || field.MaxLength != ProjectNameMaxLength {
			t.Fatal("invalid Project name accepted", err)
		}
		if _, err := s.RenameProject(t.Context(), RenameProject{ID: "11111111-1111-4111-8111-111111111111", Name: name}); !errors.As(err, &field) {
			t.Fatal("invalid Project rename accepted", err)
		}
	}
	for _, name := range []string{"", " ", "with\ncontrol", strings.Repeat("x", KeyNameMaxLength+1)} {
		var field *NameError
		if _, err := s.CreateAPIKey(t.Context(), CreateAPIKey{ProjectID: "11111111-1111-4111-8111-111111111111", ID: "22222222-2222-4222-8222-222222222222", Name: name}); !errors.As(err, &field) || field.MaxLength != KeyNameMaxLength {
			t.Fatal("invalid key name accepted", err)
		}
	}
}

func TestCreateProjectDerivesTheKeyPrincipal(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	var stored NewProject
	s := newTestService(t, &fakeStorage{createProject: func(_ context.Context, project NewProject) (Project, error) {
		stored = project
		return Project{ID: project.ID, Name: project.Name}, nil
	}})
	if _, err := s.CreateProject(t.Context(), CreateProject{ID: id, Name: "  Default  "}); err != nil {
		t.Fatal(err)
	}
	if stored != (NewProject{ID: id, Name: "Default", OrganizationID: "core", ExternalProjectID: "proj_" + id, SubjectID: "project:" + id}) {
		t.Fatalf("stored %#v", stored)
	}
}

func TestCreateAPIKeyStoresOnlyTheDigestOfAnActiveProjectKey(t *testing.T) {
	var stored NewAPIKey
	tx := &fakeKeyIssuance{
		loadProject: func(context.Context) (LockedProject, error) { return LockedProject{}, nil },
		applyAPIKey: func(_ context.Context, key NewAPIKey) (APIKey, error) {
			stored = key
			return APIKey{ID: key.ID, Name: key.Name, Prefix: key.Prefix}, nil
		},
	}
	tx.t = t
	s := newTestService(t, &fakeStorage{withKeyIssuance: func(_ context.Context, projectID string, issue func(KeyIssuanceTx) error) error {
		if projectID != "project" {
			t.Fatal("wrong Project", projectID)
		}
		return issue(tx)
	}})
	issued, err := s.CreateAPIKey(t.Context(), CreateAPIKey{ProjectID: "project", ID: "key", Name: " SDK "})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(issued.Key, "pc_"))
	if !strings.HasPrefix(issued.Key, "pc_") || err != nil || len(raw) != 32 {
		t.Fatal("secret format changed", issued.Key)
	}
	if stored.ID != "key" || stored.Name != "SDK" || stored.Prefix != issued.Key[:11] || stored.Digest != sha256.Sum256([]byte(issued.Key)) || issued.Prefix != stored.Prefix {
		t.Fatalf("stored %#v for %#v", stored, issued)
	}
}

func TestArchivedProjectIssuesNoKey(t *testing.T) {
	tx := &fakeKeyIssuance{loadProject: func(context.Context) (LockedProject, error) { return LockedProject{Archived: true}, nil }}
	tx.t = t
	s := newTestService(t, &fakeStorage{withKeyIssuance: func(_ context.Context, _ string, issue func(KeyIssuanceTx) error) error { return issue(tx) }})
	if issued, err := s.CreateAPIKey(t.Context(), CreateAPIKey{ProjectID: "project", ID: "key", Name: "late"}); !errors.Is(err, ErrArchived) || issued.Key != "" {
		t.Fatal("archived Project admitted a key", err)
	}
}

func TestStorageErrorsReturnWithoutASecret(t *testing.T) {
	s := newTestService(t, &fakeStorage{withKeyIssuance: func(context.Context, string, func(KeyIssuanceTx) error) error { return ErrNotFound }})
	if issued, err := s.CreateAPIKey(t.Context(), CreateAPIKey{ProjectID: "missing", ID: "key", Name: "key"}); !errors.Is(err, ErrNotFound) || issued.Key != "" {
		t.Fatal("failed issuance exposed a secret", err)
	}
}
