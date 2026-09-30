package sessions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
)

func (s *fakeStorage) GetEnvironment(_ context.Context, tenant, environment string) (Environment, error) {
	s.record("GetEnvironment", s.getEnvironment != nil, tenant, environment)
	return s.getEnvironment()
}

func (s *fakeStorage) LoadProjectArchived(_ context.Context, tenant string) (bool, error) {
	s.record("LoadProjectArchived", s.loadProjectArchived != nil, tenant)
	return s.loadProjectArchived()
}

func (s *fakeStorage) ExecutorProjectExists(_ context.Context, scope identity.ProjectScope) (bool, error) {
	s.record("ExecutorProjectExists", s.projectExists != nil, scope.TenantID)
	return s.projectExists()
}

func (s *fakeStorage) LoadExecutorCredentialRestriction(_ context.Context, principal identity.Principal, key string) (string, error) {
	s.record("LoadExecutorCredentialRestriction", s.loadRestriction != nil, principal.TenantID, key)
	return s.loadRestriction()
}

// signature is the fake signature of an installation payload.
func (s *fakeStorage) signature(payload string) string {
	sum := sha256.Sum256([]byte(s.installationKey + "." + payload))
	return hex.EncodeToString(sum[:])
}

func (s *fakeStorage) SignInstallation(_ context.Context, payload string) (string, error) {
	s.record("SignInstallation", s.installationKey != "")
	return s.signature(payload), nil
}

func (s *fakeStorage) VerifyInstallation(_ context.Context, payload, signature string) error {
	s.record("VerifyInstallation", s.installationKey != "" || s.verifyInstallation != nil)
	if s.verifyInstallation != nil {
		return s.verifyInstallation
	}
	if signature != s.signature(payload) {
		return ErrInstallationAuthorization
	}
	return nil
}

func (s *fakeStorage) WithExecutorCredentials(ctx context.Context, tenant string, apply func(context.Context, ExecutorCredentialTx) error) error {
	s.record("WithExecutorCredentials", s.credentials != nil, tenant)
	return apply(ctx, s.credentials)
}

func (s *fakeStorage) WithEnvironmentExecutorCredentials(ctx context.Context, tenant, environment string, apply func(context.Context, EnvironmentExecutorCredentialTx, LockedSession) error) error {
	s.record("WithEnvironmentExecutorCredentials", s.credentials != nil, tenant, environment)
	return apply(ctx, s.credentials, s.locked)
}

func (f *fakeTx) LockProject(context.Context) (bool, error) {
	f.record("LockProject", f.lockProject != nil)
	return f.lockProject()
}

func (f *fakeTx) ListExecutorCredentials(_ context.Context, environment string, subject identity.Subject) ([]ExecutorCredential, error) {
	f.record("ListExecutorCredentials", f.listExecutorCredentials != nil, environment, subject.ID)
	return f.listExecutorCredentials()
}

func (f *fakeTx) AuthenticateExecutor(_ context.Context, environment, digest string) (bool, error) {
	f.record("AuthenticateExecutor", f.authenticateExecutor != nil, environment, digest)
	return f.authenticateExecutor()
}

func (f *fakeTx) IssueExecutorCredential(_ context.Context, grant ExecutorCredentialGrant) (IssuedExecutorCredential, error) {
	f.record("IssueExecutorCredential", f.issueExecutorCredential != nil, grant.Principal.SubjectID, grant.KeyID, grant.EnvironmentID)
	return f.issueExecutorCredential(grant)
}

func (f *fakeTx) RotateExecutorCredential(_ context.Context, subject identity.Subject, key, digest string) (IssuedExecutorCredential, error) {
	f.record("RotateExecutorCredential", f.rotateExecutorCredential != nil, subject.ID, key)
	return f.rotateExecutorCredential(digest)
}

func (f *fakeTx) RevokeExecutorCredential(_ context.Context, subject identity.Subject, key string) error {
	f.record("RevokeExecutorCredential", f.revokeExecutorCredential != nil, subject.ID, key)
	return f.revokeExecutorCredential()
}

func (f *fakeTx) RecordExecutorCredentialAudit(_ context.Context, action, key string) error {
	f.record("RecordExecutorCredentialAudit", f.recordExecutorCredentialAudit != nil, action, key)
	return f.recordExecutorCredentialAudit()
}

func (f *fakeTx) LoadSessionCreator(context.Context) (identity.Subject, bool, error) {
	f.record("LoadSessionCreator", f.loadSessionCreator != nil)
	return f.loadSessionCreator()
}

// fails is a fake method that fails with err.
func fails[T any](err error) func() (T, error) {
	return func() (T, error) {
		var zero T
		return zero, err
	}
}

const (
	testKey         = "0d9a4f3e-5b1c-4c2e-8f6a-7e3b2d1c0a99"
	testEnvironment = "2b5e8c1d-4f3a-4e6b-9c7d-8a1f0e2d3c4b"
)

var (
	testPrincipal = identity.Principal{ProjectScope: identity.ProjectScope{TenantID: testTenant, OrganizationID: "organization", ProjectID: "project"}, SubjectKind: "user", SubjectID: "creator"}
	selfHosted    = Environment{ID: testEnvironment, SessionID: testSession, Status: "ready", Configuration: []byte(`{"type":"self_hosted"}`)}
	// theCreator is a fake LoadSessionCreator: testPrincipal created the
	// Session.
	theCreator = loads(testPrincipal.Subject(), true)
)

// credentialCase is one executor credential use case: the storage and
// transaction it runs against, what it returns and the calls it makes.
type credentialCase struct {
	name         string
	storage      fakeStorage
	tx           fakeTx
	operate      func(*Service) (IssuedExecutorCredential, error)
	want         error
	storageCalls []string
	txCalls      []string
}

// runCredentialCases runs each case and checks its error, calls and issued secret: a
// successful issuance or rotation returns the token whose digest it stored.
func runCredentialCases(t *testing.T, cases []credentialCase, digest *string) {
	t.Helper()
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			*digest = ""
			tx := test.tx
			tx.t = t
			storage := test.storage
			storage.t, storage.credentials = t, &tx
			service, err := NewService(&storage)
			if err != nil {
				t.Fatal(err)
			}
			issued, err := test.operate(service)
			if test.want == nil && err != nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			assertStorageCalls(t, &storage, test.storageCalls...)
			assertCalls(t, &tx, test.txCalls...)
			if (issued.Token != "" || *digest != "") && (err != nil || executorDigest(issued.Token) != *digest) {
				t.Fatalf("issued %+v with digest %q, %v", issued, *digest, err)
			}
		})
	}
}

func TestExecutorCredentialUseCases(t *testing.T) {
	var digest string
	issued := func(grant ExecutorCredentialGrant) (IssuedExecutorCredential, error) {
		digest = grant.Digest
		return IssuedExecutorCredential{KeyID: grant.KeyID, EnvironmentID: grant.EnvironmentID}, nil
	}
	rotated := func(stored string) (IssuedExecutorCredential, error) {
		digest = stored
		return IssuedExecutorCredential{KeyID: testKey}, nil
	}
	issue := func(environment string) func(*Service) (IssuedExecutorCredential, error) {
		return func(s *Service) (IssuedExecutorCredential, error) {
			return s.IssueExecutorCredential(t.Context(), testPrincipal, testKey, environment)
		}
	}
	rotate := func(s *Service) (IssuedExecutorCredential, error) {
		return s.RotateExecutorCredential(t.Context(), testPrincipal, testKey)
	}
	revoke := func(s *Service) (IssuedExecutorCredential, error) {
		return IssuedExecutorCredential{}, s.RevokeExecutorCredential(t.Context(), testPrincipal, testKey)
	}
	exists := "ExecutorProjectExists " + testTenant
	restriction := "LoadExecutorCredentialRestriction " + testTenant + " " + testKey
	unrestricted := "WithExecutorCredentials " + testTenant
	restricted := "WithEnvironmentExecutorCredentials " + testTenant + " " + testEnvironment
	issuance := func(environment string) string {
		return strings.Join([]string{"IssueExecutorCredential creator", testKey, environment}, " ")
	}
	rotation := "RotateExecutorCredential creator " + testKey
	mapped := fakeStorage{projectExists: returns(true)}
	runCredentialCases(t, []credentialCase{
		{"issues an unrestricted key", mapped, fakeTx{issueExecutorCredential: issued}, issue(""), nil,
			[]string{exists, unrestricted}, []string{issuance("")}},
		{"issues a key in the creator's Session", mapped, fakeTx{loadSessionCreator: theCreator, issueExecutorCredential: issued}, issue(testEnvironment), nil,
			[]string{exists, restricted}, []string{"LoadSessionCreator", issuance(testEnvironment)}},
		{"another creator's Session", mapped, fakeTx{loadSessionCreator: loads(identity.Subject{Kind: "user", ID: "other"}, true)}, issue(testEnvironment), ErrNotFound,
			[]string{exists, restricted}, []string{"LoadSessionCreator"}},
		{"a Session without a creator", mapped, fakeTx{loadSessionCreator: loads(identity.Subject{}, false)}, issue(testEnvironment), ErrNotFound,
			[]string{exists, restricted}, []string{"LoadSessionCreator"}},
		{"a deleted Session", fakeStorage{projectExists: returns(true), locked: LockedSession{Deleted: true}}, fakeTx{}, issue(testEnvironment), ErrNotFound,
			[]string{exists, restricted}, nil},
		{"an unmapped Project", fakeStorage{projectExists: returns(false)}, fakeTx{}, issue(""), ErrNotFound, []string{exists}, nil},
		{"a malformed Environment", mapped, fakeTx{}, issue("environment"), ErrInvalidInput, []string{exists}, nil},
		{"an existing key", mapped, fakeTx{issueExecutorCredential: func(ExecutorCredentialGrant) (IssuedExecutorCredential, error) {
			return IssuedExecutorCredential{}, ErrExecutorCredentialExists
		}}, issue(""), ErrExecutorCredentialExists, []string{exists, unrestricted}, []string{issuance("")}},
		{"rotates in the restricted Environment's Session", fakeStorage{loadRestriction: returns(testEnvironment)}, fakeTx{loadSessionCreator: theCreator, rotateExecutorCredential: rotated}, rotate, nil,
			[]string{restriction, restricted}, []string{"LoadSessionCreator", rotation}},
		{"rotates an unrestricted key", fakeStorage{loadRestriction: returns("")}, fakeTx{rotateExecutorCredential: rotated}, rotate, nil,
			[]string{restriction, unrestricted}, []string{rotation}},
		{"rotates an unknown key", fakeStorage{loadRestriction: fails[string](ErrNotFound)}, fakeTx{}, rotate, ErrNotFound, []string{restriction}, nil},
		{"revokes", fakeStorage{loadRestriction: returns(testEnvironment)}, fakeTx{revokeExecutorCredential: done}, revoke, nil,
			[]string{restriction, unrestricted}, []string{"RevokeExecutorCredential creator " + testKey}},
		{"revokes an unknown key", fakeStorage{loadRestriction: fails[string](ErrNotFound)}, fakeTx{}, revoke, ErrNotFound, []string{restriction}, nil},
	}, &digest)

	// A malformed principal or key is rejected before storage.
	service := deviceService(t, &fakeStorage{t: t})
	invalid := testPrincipal
	invalid.SubjectKind = "robot"
	if _, err := service.IssueExecutorCredential(t.Context(), invalid, testKey, ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid principal: %v", err)
	}
	if _, err := service.RotateExecutorCredential(t.Context(), testPrincipal, "key"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("malformed key: %v", err)
	}
	if err := service.RevokeExecutorCredential(t.Context(), testPrincipal, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("nil key: %v", err)
	}
}

func TestProjectExecutorCredentialUseCases(t *testing.T) {
	var digest string
	issued := func(grant ExecutorCredentialGrant) (IssuedExecutorCredential, error) {
		digest = grant.Digest
		return IssuedExecutorCredential{KeyID: grant.KeyID, EnvironmentID: grant.EnvironmentID}, nil
	}
	rotated := func(stored string) (IssuedExecutorCredential, error) {
		digest = stored
		return IssuedExecutorCredential{KeyID: testKey, EnvironmentID: testEnvironment}, nil
	}
	operate := func(rotate bool) func(*Service) (IssuedExecutorCredential, error) {
		return func(s *Service) (IssuedExecutorCredential, error) {
			return s.IssueProjectExecutorCredential(t.Context(), testPrincipal, testEnvironment, testKey, rotate)
		}
	}
	revoke := func(s *Service) (IssuedExecutorCredential, error) {
		return IssuedExecutorCredential{}, s.RevokeProjectExecutorCredential(t.Context(), testPrincipal, testEnvironment, testKey)
	}
	target := "GetEnvironment " + testTenant + " " + testEnvironment
	archive := "LoadProjectArchived " + testTenant
	restriction := "LoadExecutorCredentialRestriction " + testTenant + " " + testKey
	restricted := "WithEnvironmentExecutorCredentials " + testTenant + " " + testEnvironment
	active := fakeStorage{getEnvironment: returns(selfHosted), loadProjectArchived: returns(false), projectExists: returns(true), loadRestriction: returns(testEnvironment)}
	withStorage := func(change func(*fakeStorage)) fakeStorage {
		storage := active
		change(&storage)
		return storage
	}
	runCredentialCases(t, []credentialCase{
		{"issues and audits", active, fakeTx{loadSessionCreator: theCreator, lockProject: returns(false), recordExecutorCredentialAudit: done, issueExecutorCredential: issued}, operate(false), nil,
			[]string{target, archive, "ExecutorProjectExists " + testTenant, restricted},
			[]string{"LoadSessionCreator", "LockProject", "RecordExecutorCredentialAudit issue " + testKey, "IssueExecutorCredential creator " + testKey + " " + testEnvironment}},
		{"rotates and audits", active, fakeTx{loadSessionCreator: theCreator, lockProject: returns(false), recordExecutorCredentialAudit: done, rotateExecutorCredential: rotated}, operate(true), nil,
			[]string{target, archive, restriction, restriction, restricted},
			[]string{"LoadSessionCreator", "LockProject", "RecordExecutorCredentialAudit rotate " + testKey, "RotateExecutorCredential creator " + testKey}},
		{"an OpenAI-hosted Environment", withStorage(func(s *fakeStorage) { s.getEnvironment = returns(hostedEnvironment) }), fakeTx{}, operate(false), ErrNotFound,
			[]string{target}, nil},
		{"an archived Project", withStorage(func(s *fakeStorage) { s.loadProjectArchived = returns(true) }), fakeTx{}, operate(false), projects.ErrArchived,
			[]string{target, archive}, nil},
		{"archived before the write", active, fakeTx{loadSessionCreator: theCreator, lockProject: returns(true)}, operate(false), projects.ErrArchived,
			[]string{target, archive, "ExecutorProjectExists " + testTenant, restricted}, []string{"LoadSessionCreator", "LockProject"}},
		{"rotates a key restricted elsewhere", withStorage(func(s *fakeStorage) { s.loadRestriction = returns("") }), fakeTx{}, operate(true), ErrNotFound,
			[]string{target, archive, restriction}, nil},
		{"revokes and audits in an archived Project", withStorage(func(s *fakeStorage) { s.loadProjectArchived = nil }), fakeTx{revokeExecutorCredential: done, recordExecutorCredentialAudit: done}, revoke, nil,
			[]string{target, restriction, "WithExecutorCredentials " + testTenant},
			[]string{"RevokeExecutorCredential creator " + testKey, "RecordExecutorCredentialAudit revoke " + testKey}},
		{"revokes a key restricted elsewhere", withStorage(func(s *fakeStorage) { s.loadRestriction = returns("6c3b2a19-8d7e-4f60-a5b4-c3d2e1f0a9b8") }), fakeTx{}, revoke, ErrNotFound,
			[]string{target, restriction}, nil},
	}, &digest)
}
