package skills

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeStorage struct {
	t                   *testing.T
	createSkill         func(context.Context, NewSkill) (Skill, error)
	createVersion       func(context.Context, NewVersion) (Version, error)
	setDefaultVersion   func(context.Context, string, uuid.UUID, int64) (Skill, error)
	deleteSkill         func(context.Context, string, uuid.UUID) error
	withVersionDeletion func(context.Context, string, uuid.UUID, func(VersionDeletionTx) error) error
}

func unexpectedCall(t *testing.T, method string) {
	t.Helper()
	t.Fatalf("unexpected call to %s", method)
}

func (f *fakeStorage) CreateSkill(ctx context.Context, skill NewSkill) (Skill, error) {
	if f.createSkill == nil {
		unexpectedCall(f.t, "CreateSkill")
	}
	return f.createSkill(ctx, skill)
}

func (f *fakeStorage) CreateVersion(ctx context.Context, version NewVersion) (Version, error) {
	if f.createVersion == nil {
		unexpectedCall(f.t, "CreateVersion")
	}
	return f.createVersion(ctx, version)
}

func (f *fakeStorage) SetDefaultVersion(ctx context.Context, tenantID string, skillID uuid.UUID, version int64) (Skill, error) {
	if f.setDefaultVersion == nil {
		unexpectedCall(f.t, "SetDefaultVersion")
	}
	return f.setDefaultVersion(ctx, tenantID, skillID, version)
}

func (f *fakeStorage) DeleteSkill(ctx context.Context, tenantID string, skillID uuid.UUID) error {
	if f.deleteSkill == nil {
		unexpectedCall(f.t, "DeleteSkill")
	}
	return f.deleteSkill(ctx, tenantID, skillID)
}

func (f *fakeStorage) WithVersionDeletion(ctx context.Context, tenantID string, skillID uuid.UUID, apply func(VersionDeletionTx) error) error {
	if f.withVersionDeletion == nil {
		unexpectedCall(f.t, "WithVersionDeletion")
	}
	return f.withVersionDeletion(ctx, tenantID, skillID, apply)
}

type fakeVersionDeletionTx struct {
	t     *testing.T
	load  func(int64) (VersionDeletionFacts, error)
	apply func(VersionDeletion) error
}

func (f *fakeVersionDeletionTx) LoadVersionDeletion(version int64) (VersionDeletionFacts, error) {
	if f.load == nil {
		unexpectedCall(f.t, "LoadVersionDeletion")
	}
	return f.load(version)
}

func (f *fakeVersionDeletionTx) ApplyVersionDeletion(decision VersionDeletion) error {
	if f.apply == nil {
		unexpectedCall(f.t, "ApplyVersionDeletion")
	}
	return f.apply(decision)
}

type fakeReader struct {
	t                     *testing.T
	skill                 func(context.Context, string, uuid.UUID) (Skill, error)
	skills                func(context.Context, string, SkillPageQuery) (Page, error)
	version               func(context.Context, string, uuid.UUID, int64) (Version, error)
	versionByID           func(context.Context, string, uuid.UUID) (Version, error)
	versions              func(context.Context, string, uuid.UUID, VersionPageQuery) (VersionPage, error)
	versionContent        func(context.Context, string, uuid.UUID, int64) (Content, error)
	defaultVersionContent func(context.Context, string, uuid.UUID) (Content, error)
}

func (f *fakeReader) Skill(ctx context.Context, tenantID string, id uuid.UUID) (Skill, error) {
	if f.skill == nil {
		unexpectedCall(f.t, "Skill")
	}
	return f.skill(ctx, tenantID, id)
}

func (f *fakeReader) Skills(ctx context.Context, tenantID string, page SkillPageQuery) (Page, error) {
	if f.skills == nil {
		unexpectedCall(f.t, "Skills")
	}
	return f.skills(ctx, tenantID, page)
}

func (f *fakeReader) Version(ctx context.Context, tenantID string, skillID uuid.UUID, version int64) (Version, error) {
	if f.version == nil {
		unexpectedCall(f.t, "Version")
	}
	return f.version(ctx, tenantID, skillID, version)
}

func (f *fakeReader) VersionByID(ctx context.Context, tenantID string, id uuid.UUID) (Version, error) {
	if f.versionByID == nil {
		unexpectedCall(f.t, "VersionByID")
	}
	return f.versionByID(ctx, tenantID, id)
}

func (f *fakeReader) Versions(ctx context.Context, tenantID string, skillID uuid.UUID, page VersionPageQuery) (VersionPage, error) {
	if f.versions == nil {
		unexpectedCall(f.t, "Versions")
	}
	return f.versions(ctx, tenantID, skillID, page)
}

func (f *fakeReader) VersionContent(ctx context.Context, tenantID string, skillID uuid.UUID, version int64) (Content, error) {
	if f.versionContent == nil {
		unexpectedCall(f.t, "VersionContent")
	}
	return f.versionContent(ctx, tenantID, skillID, version)
}

func (f *fakeReader) DefaultVersionContent(ctx context.Context, tenantID string, skillID uuid.UUID) (Content, error) {
	if f.defaultVersionContent == nil {
		unexpectedCall(f.t, "DefaultVersionContent")
	}
	return f.defaultVersionContent(ctx, tenantID, skillID)
}

func newTestService(t *testing.T) (*Service, *fakeStorage, *fakeReader) {
	t.Helper()
	storage, reader := &fakeStorage{t: t}, &fakeReader{t: t}
	service, err := NewService(storage, reader)
	if err != nil {
		t.Fatal(err)
	}
	return service, storage, reader
}

func TestNewServiceRequiresDependencies(t *testing.T) {
	if _, err := NewService(nil, &fakeReader{t: t}); err == nil {
		t.Fatal("nil storage accepted")
	}
	if _, err := NewService(&fakeStorage{t: t}, nil); err == nil {
		t.Fatal("nil reader accepted")
	}
}

// Uploads take their name and description from the archive, and an invalid
// archive is rejected before storage sees it, even for a missing Skill.
func TestUploadsValidateTheArchiveFirst(t *testing.T) {
	service, storage, _ := newTestService(t)
	archive, skill := testArchive(t, "proof", "Verify a Skill."), uuid.New()
	for name, upload := range map[string]func() error{
		"skill": func() error {
			_, err := service.CreateSkill(t.Context(), CreateSkill{TenantID: "tenant", Archive: []byte("not a zip")})
			return err
		},
		"version": func() error {
			_, err := service.CreateVersion(t.Context(), CreateVersion{TenantID: "tenant", SkillID: skill, Archive: nil})
			return err
		},
	} {
		if err := upload(); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	storage.createSkill = func(_ context.Context, got NewSkill) (Skill, error) {
		if !reflect.DeepEqual(got, NewSkill{TenantID: "tenant", Name: "proof", Description: "Verify a Skill.", Archive: archive}) {
			t.Fatalf("new skill = %+v", got)
		}
		return Skill{ID: "skill_created"}, nil
	}
	if created, err := service.CreateSkill(t.Context(), CreateSkill{TenantID: "tenant", Archive: archive}); err != nil || created.ID != "skill_created" {
		t.Fatalf("create = %+v, %v", created, err)
	}
	storage.createVersion = func(_ context.Context, got NewVersion) (Version, error) {
		if !reflect.DeepEqual(got, NewVersion{TenantID: "tenant", SkillID: skill, Name: "proof", Description: "Verify a Skill.", Archive: archive, MakeDefault: true}) {
			t.Fatalf("new version = %+v", got)
		}
		return Version{}, ErrNotFound
	}
	if _, err := service.CreateVersion(t.Context(), CreateVersion{TenantID: "tenant", SkillID: skill, Archive: archive, MakeDefault: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("version = %v", err)
	}
}

// The default version must be a concrete version number; selectors are
// rejected before storage.
func TestSetDefaultVersionRequiresAConcreteVersion(t *testing.T) {
	service, storage, _ := newTestService(t)
	skill := uuid.New()
	for _, value := range []string{"", "latest", "0", "01", "-1", "x"} {
		if _, err := service.SetDefaultVersion(t.Context(), SetDefaultVersion{TenantID: "tenant", SkillID: skill, Version: value}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%q: %v", value, err)
		}
	}
	storage.setDefaultVersion = func(_ context.Context, tenant string, id uuid.UUID, version int64) (Skill, error) {
		if tenant != "tenant" || id != skill || version != 3 {
			t.Fatalf("set default = %s %s %d", tenant, id, version)
		}
		return Skill{DefaultVersion: 3}, nil
	}
	if updated, err := service.SetDefaultVersion(t.Context(), SetDefaultVersion{TenantID: "tenant", SkillID: skill, Version: "3"}); err != nil || updated.DefaultVersion != 3 {
		t.Fatalf("set default = %+v, %v", updated, err)
	}
}

func TestDeleteVersionDecidesUnderTheLock(t *testing.T) {
	skill := uuid.New()
	for _, test := range []struct {
		name    string
		facts   VersionDeletionFacts
		loadErr error
		applied *VersionDeletion
		err     error
	}{
		{"missing version", VersionDeletionFacts{}, ErrNotFound, nil, ErrNotFound},
		{"default with others", VersionDeletionFacts{Skill: Skill{DefaultVersion: 2, LatestVersion: 3}, Target: Version{Version: 2}, OthersRemain: true}, nil, nil, ErrDefaultVersion},
		{"latest", VersionDeletionFacts{Skill: Skill{DefaultVersion: 1, LatestVersion: 2}, Target: Version{ID: "skillver_x", Version: 2}, OthersRemain: true}, nil, &VersionDeletion{Target: Version{ID: "skillver_x", Version: 2}, RefreshLatest: true}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, storage, _ := newTestService(t)
			var applied *VersionDeletion
			storage.withVersionDeletion = func(_ context.Context, tenant string, id uuid.UUID, apply func(VersionDeletionTx) error) error {
				if tenant != "tenant" || id != skill {
					t.Fatalf("lock = %s %s", tenant, id)
				}
				tx := &fakeVersionDeletionTx{t: t, load: func(version int64) (VersionDeletionFacts, error) {
					if version != 2 {
						t.Fatalf("load version %d", version)
					}
					return test.facts, test.loadErr
				}}
				if test.applied != nil {
					tx.apply = func(decision VersionDeletion) error { applied = &decision; return nil }
				}
				return apply(tx)
			}
			deleted, err := service.DeleteVersion(t.Context(), DeleteVersion{TenantID: "tenant", SkillID: skill, Version: 2})
			if !errors.Is(err, test.err) || !reflect.DeepEqual(applied, test.applied) {
				t.Fatalf("deleted = %+v, %v; applied %+v", deleted, err, applied)
			}
			if test.applied != nil && deleted != test.applied.Target {
				t.Fatalf("deleted = %+v", deleted)
			}
		})
	}
}

func TestListSkills(t *testing.T) {
	service, _, reader := newTestService(t)
	for _, limit := range []int{-1, MaxPageLimit + 1} {
		if _, err := service.ListSkills(t.Context(), ListSkills{TenantID: "tenant", Limit: limit}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	// A malformed cursor resolves like a missing Skill.
	reader.skill = func(_ context.Context, _ string, id uuid.UUID) (Skill, error) {
		if id != uuid.Max {
			t.Fatalf("cursor lookup %s", id)
		}
		return Skill{}, ErrNotFound
	}
	if _, err := service.ListSkills(t.Context(), ListSkills{TenantID: "tenant", After: "skill_bad", Limit: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed cursor: %v", err)
	}
	cursor, created := uuid.New(), time.Unix(100, 0)
	reader.skill = func(context.Context, string, uuid.UUID) (Skill, error) { return Skill{CreatedAt: created}, nil }
	reader.skills = func(_ context.Context, tenant string, page SkillPageQuery) (Page, error) {
		want := SkillPageQuery{After: &SkillCursor{CreatedAt: created, ID: cursor}, Limit: 0, Ascending: true}
		if tenant != "tenant" || !reflect.DeepEqual(page, want) {
			t.Fatalf("page = %+v", page)
		}
		return Page{HasMore: true}, nil
	}
	if page, err := service.ListSkills(t.Context(), ListSkills{TenantID: "tenant", After: FormatID(cursor), Limit: 0, Ascending: true}); err != nil || !page.HasMore {
		t.Fatalf("page = %+v, %v", page, err)
	}
}

func TestListVersionsResolvesTheCursor(t *testing.T) {
	skill, other, cursor := uuid.New(), uuid.New(), uuid.New()
	for _, test := range []struct {
		name        string
		after       string
		skillErrs   []error
		cursorSkill uuid.UUID
		cursorErr   error
		err         error
		message     string
	}{
		{name: "missing skill", after: "anything", skillErrs: []error{ErrNotFound}, err: ErrNotFound},
		{name: "not a version ID", after: "skill_x", skillErrs: []error{nil}, message: "Invalid 'after': 'skill_x'. Expected an ID that begins with 'skillver'."},
		{name: "malformed version ID", after: "skillver_x", skillErrs: []error{nil}, err: ErrNotFound},
		{name: "missing version", after: FormatVersionID(cursor), skillErrs: []error{nil}, cursorErr: ErrNotFound, err: ErrNotFound},
		{name: "other skill", after: FormatVersionID(cursor), skillErrs: []error{nil, nil}, cursorSkill: other, message: "Skill version cursor does not match this skill."},
		{name: "skill deleted meanwhile", after: FormatVersionID(cursor), skillErrs: []error{nil, ErrNotFound}, cursorSkill: other, err: ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _, reader := newTestService(t)
			reader.skill = func(_ context.Context, _ string, id uuid.UUID) (Skill, error) {
				if id != skill || len(test.skillErrs) == 0 {
					t.Fatalf("unexpected Skill lookup %s", id)
				}
				err := test.skillErrs[0]
				test.skillErrs = test.skillErrs[1:]
				return Skill{}, err
			}
			if test.cursorSkill != uuid.Nil || test.cursorErr != nil {
				reader.versionByID = func(_ context.Context, tenant string, id uuid.UUID) (Version, error) {
					if tenant != "tenant" || id != cursor {
						t.Fatalf("cursor lookup %s %s", tenant, id)
					}
					return Version{SkillID: FormatID(test.cursorSkill)}, test.cursorErr
				}
			}
			_, err := service.ListVersions(t.Context(), ListVersions{TenantID: "tenant", SkillID: skill, After: test.after, Limit: 5})
			var cursorErr *CursorError
			if test.message != "" {
				if !errors.As(err, &cursorErr) || cursorErr.Message != test.message {
					t.Fatalf("err = %v", err)
				}
			} else if !errors.Is(err, test.err) {
				t.Fatalf("err = %v", err)
			}
			if len(test.skillErrs) != 0 {
				t.Fatalf("Skill lookups left: %v", test.skillErrs)
			}
		})
	}
}

func TestListVersionsPagesAfterTheCursorVersion(t *testing.T) {
	service, _, reader := newTestService(t)
	for _, limit := range []int{-1, MaxPageLimit + 1} {
		if _, err := service.ListVersions(t.Context(), ListVersions{TenantID: "tenant", SkillID: uuid.New(), Limit: limit}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	skill, cursor := uuid.New(), uuid.New()
	reader.skill = func(context.Context, string, uuid.UUID) (Skill, error) { return Skill{}, nil }
	reader.versionByID = func(context.Context, string, uuid.UUID) (Version, error) {
		return Version{SkillID: FormatID(skill), Version: 4}, nil
	}
	reader.versions = func(_ context.Context, _ string, id uuid.UUID, page VersionPageQuery) (VersionPage, error) {
		if id != skill || page != (VersionPageQuery{AfterVersion: 4, Limit: 2}) {
			t.Fatalf("page = %s %+v", id, page)
		}
		return VersionPage{HasMore: true}, nil
	}
	if page, err := service.ListVersions(t.Context(), ListVersions{TenantID: "tenant", SkillID: skill, After: FormatVersionID(cursor), Limit: 2}); err != nil || !page.HasMore {
		t.Fatalf("page = %+v, %v", page, err)
	}
}

// Content reads verify the decrypted archive against the version record.
func TestReadsVerifyContent(t *testing.T) {
	service, _, reader := newTestService(t)
	skill := uuid.New()
	good := Content{Version: Version{Name: "proof", Description: "Verify a Skill."}, Archive: testArchive(t, "proof", "Verify a Skill.")}
	tampered := Content{Version: Version{Name: "other", Description: "Verify a Skill."}, Archive: good.Archive}
	reader.versionContent = func(_ context.Context, _ string, id uuid.UUID, version int64) (Content, error) {
		if id != skill {
			t.Fatalf("read %s", id)
		}
		if version == 1 {
			return good, nil
		}
		return tampered, nil
	}
	if content, err := service.ReadVersion(t.Context(), ReadVersion{TenantID: "tenant", SkillID: skill, Version: 1}); err != nil || !reflect.DeepEqual(content, good) {
		t.Fatalf("read = %v", err)
	}
	if content, err := service.ReadVersion(t.Context(), ReadVersion{TenantID: "tenant", SkillID: skill, Version: 2}); !errors.Is(err, ErrInvalidInput) || content.Archive != nil {
		t.Fatalf("tampered read = %v", err)
	}
	reader.defaultVersionContent = func(context.Context, string, uuid.UUID) (Content, error) { return Content{}, ErrNotFound }
	if _, err := service.ReadDefaultVersion(t.Context(), ReadDefaultVersion{TenantID: "tenant", SkillID: skill}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("default read = %v", err)
	}
}
