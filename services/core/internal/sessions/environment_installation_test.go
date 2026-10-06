package sessions

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
)

// installationToken signs claim as storage does.
func installationToken(t *testing.T, storage *fakeStorage, claim InstallationAuthorization) string {
	t.Helper()
	payload, err := json.Marshal(claim)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + storage.signature(encoded)
}

func assertStorageCalls(t *testing.T, storage *fakeStorage, want ...string) {
	t.Helper()
	if strings.Join(storage.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("storage calls:\n%s\nwant:\n%s", strings.Join(storage.calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestEnvironmentInstallationAuthorization(t *testing.T) {
	target := "GetEnvironment " + testTenant + " " + testEnvironment
	archive := "LoadProjectArchived " + testTenant
	installable := func() *fakeStorage {
		return &fakeStorage{t: t, installationKey: "key", getEnvironment: returns(selfHosted), loadProjectArchived: returns(false)}
	}
	storage := installable()
	service := deviceService(t, storage)
	token, expires, err := service.AuthorizeEnvironmentInstallation(t.Context(), testPrincipal, testEnvironment, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if lifetime := time.Until(time.Unix(expires, 0)); lifetime <= 29*time.Minute || lifetime > 30*time.Minute {
		t.Fatalf("expires in %s", lifetime)
	}
	claim, err := service.ValidateEnvironmentInstallation(t.Context(), token, "1.2.3")
	if want := (InstallationAuthorization{Principal: testPrincipal, Environment: testEnvironment, Version: "1.2.3", ExpiresAt: expires}); err != nil || claim != want {
		t.Fatalf("claim %+v, %v", claim, err)
	}
	assertStorageCalls(t, storage, target, archive, "SignInstallation", "VerifyInstallation", target, archive)

	encoded, signature, _ := strings.Cut(token, ".")
	expired := installationToken(t, storage, InstallationAuthorization{Principal: testPrincipal, Environment: testEnvironment, Version: "1.2.3", ExpiresAt: time.Now().Add(-time.Second).Unix()})
	for _, test := range []struct {
		name, token, version string
		storage              *fakeStorage
	}{
		{"another version", token, "1.2.4", installable()},
		{"another signature", encoded + "." + strings.Repeat("0", len(signature)), "1.2.3", installable()},
		{"no signature", encoded, "1.2.3", installable()},
		{"too long", token + strings.Repeat("0", maxInstallationToken), "1.2.3", installable()},
		{"expired", expired, "1.2.3", installable()},
		{"deleted Environment", token, "1.2.3", &fakeStorage{t: t, installationKey: "key", getEnvironment: fails[Environment](ErrNotFound)}},
		{"archived Project", token, "1.2.3", &fakeStorage{t: t, installationKey: "key", getEnvironment: returns(selfHosted), loadProjectArchived: returns(true)}},
	} {
		if _, err := deviceService(t, test.storage).ValidateEnvironmentInstallation(t.Context(), test.token, test.version); !errors.Is(err, ErrInstallationAuthorization) {
			t.Fatalf("%s: %v", test.name, err)
		}
	}

	hosted := &fakeStorage{t: t, getEnvironment: returns(hostedEnvironment)}
	if _, _, err := deviceService(t, hosted).AuthorizeEnvironmentInstallation(t.Context(), testPrincipal, testEnvironment, "1.2.3"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("OpenAI-hosted Environment: %v", err)
	}
	archived := &fakeStorage{t: t, getEnvironment: returns(selfHosted), loadProjectArchived: returns(true)}
	if _, _, err := deviceService(t, archived).AuthorizeEnvironmentInstallation(t.Context(), testPrincipal, testEnvironment, "1.2.3"); !errors.Is(err, projects.ErrArchived) {
		t.Fatalf("archived Project: %v", err)
	}
}

func TestClaimEnvironmentInstallation(t *testing.T) {
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	digest := executorDigest(secret)
	validation := []string{"VerifyInstallation", "GetEnvironment " + testTenant + " " + testEnvironment, "LoadProjectArchived " + testTenant}
	claiming := append(validation[:len(validation):len(validation)], "WithEnvironmentExecutorCredentials "+testTenant+" "+testEnvironment)
	checked := []string{"LoadSessionCreator", "LockProject", "ListExecutorCredentials " + testEnvironment + " creator"}
	claimed := func(grant ExecutorCredentialGrant) (IssuedExecutorCredential, error) {
		if grant != (ExecutorCredentialGrant{Principal: testPrincipal, KeyID: testEnvironment, EnvironmentID: testEnvironment, Digest: digest}) {
			t.Errorf("grant %+v", grant)
		}
		return IssuedExecutorCredential{KeyID: grant.KeyID, EnvironmentID: grant.EnvironmentID}, nil
	}
	own := []ExecutorCredential{{KeyID: testEnvironment}}
	revoked := time.Unix(1, 0)
	for _, test := range []struct {
		name         string
		secret       string
		tx           fakeTx
		want         error
		storageCalls []string
		txCalls      []string
	}{
		{"claims the Environment's key", secret, fakeTx{loadSessionCreator: theCreator, lockProject: returns(false), listExecutorCredentials: returns([]ExecutorCredential{}), issueExecutorCredential: claimed},
			nil, claiming, append(checked, "IssueExecutorCredential creator "+testEnvironment+" "+testEnvironment)},
		{"retries with the same secret", secret, fakeTx{loadSessionCreator: theCreator, lockProject: returns(false), listExecutorCredentials: returns(own), authenticateExecutor: returns(true)},
			nil, claiming, append(checked, "AuthenticateExecutor "+testEnvironment+" "+digest)},
		{"another secret", secret, fakeTx{loadSessionCreator: theCreator, lockProject: returns(false), listExecutorCredentials: returns(own), authenticateExecutor: returns(false)},
			ErrExecutorCredentialExists, claiming, append(checked, "AuthenticateExecutor "+testEnvironment+" "+digest)},
		{"a revoked key", secret, fakeTx{loadSessionCreator: theCreator, lockProject: returns(false), listExecutorCredentials: returns([]ExecutorCredential{{KeyID: testEnvironment, RevokedAt: &revoked}})},
			ErrExecutorCredentialExists, claiming, checked},
		{"an operator-issued key", secret, fakeTx{loadSessionCreator: theCreator, lockProject: returns(false), listExecutorCredentials: returns([]ExecutorCredential{{KeyID: testKey}})},
			ErrExecutorCredentialExists, claiming, checked},
		{"a second key", secret, fakeTx{loadSessionCreator: theCreator, lockProject: returns(false), listExecutorCredentials: returns(append(own, ExecutorCredential{KeyID: testKey}))},
			ErrExecutorCredentialExists, claiming, checked},
		{"archived since the authorization", secret, fakeTx{loadSessionCreator: theCreator, lockProject: returns(true)},
			projects.ErrArchived, claiming, checked[:2]},
		{"another creator's Session", secret, fakeTx{loadSessionCreator: loads(identity.Subject{Kind: "user", ID: "other"}, true)},
			ErrNotFound, claiming, checked[:1]},
		{"a padded secret", secret + "=", fakeTx{}, ErrInvalidInput, validation, nil},
		{"a short secret", secret[:40], fakeTx{}, ErrInvalidInput, validation, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := test.tx
			tx.t = t
			storage := &fakeStorage{t: t, installationKey: "key", getEnvironment: returns(selfHosted), loadProjectArchived: returns(false), credentials: &tx}
			token := installationToken(t, storage, InstallationAuthorization{Principal: testPrincipal, Environment: testEnvironment, Version: "1.2.3", ExpiresAt: time.Now().Add(time.Minute).Unix()})
			err := deviceService(t, storage).ClaimEnvironmentInstallation(t.Context(), token, "1.2.3", test.secret)
			if test.want == nil && err != nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			assertStorageCalls(t, storage, test.storageCalls...)
			assertCalls(t, &tx, test.txCalls...)
		})
	}
	storage := &fakeStorage{t: t, installationKey: "key"}
	if err := deviceService(t, storage).ClaimEnvironmentInstallation(t.Context(), "payload.signature", "1.2.3", secret); !errors.Is(err, ErrInstallationAuthorization) {
		t.Fatalf("unsigned token: %v", err)
	}
	assertStorageCalls(t, storage, "VerifyInstallation")
}

// A storage failure to verify, such as a missing credential key, is not an
// invalid authorization.
func TestInstallationVerificationErrorsPassThrough(t *testing.T) {
	unavailable := errors.New("credential key unavailable")
	for name, use := range map[string]func(*Service) error{
		"validate": func(s *Service) error {
			_, err := s.ValidateEnvironmentInstallation(t.Context(), "payload.signature", "1.2.3")
			return err
		},
		"claim": func(s *Service) error {
			return s.ClaimEnvironmentInstallation(t.Context(), "payload.signature", "1.2.3", "secret")
		},
	} {
		storage := &fakeStorage{t: t, verifyInstallation: unavailable}
		if err := use(deviceService(t, storage)); err != unavailable {
			t.Fatalf("%s: %v, want the storage error unchanged", name, err)
		}
		assertStorageCalls(t, storage, "VerifyInstallation")
	}
}
