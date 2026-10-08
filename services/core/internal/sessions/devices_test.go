package sessions

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
)

// fakeStorage is strict pooled Session storage for the device and executor
// credential use cases. Each method records its call, with the arguments that
// identify it, then runs its func; a method whose func is unset fails the
// test.
type fakeStorage struct {
	t     *testing.T
	calls []string

	touchDevice              func() (bool, error)
	touchAuthenticatedDevice func() (bool, error)
	// enrollment is the transaction WithEnrollment applies in, with
	// environment and locked.
	enrollment  *fakeTx
	environment Environment
	locked      LockedSession

	getEnvironment      func() (Environment, error)
	loadProjectArchived func() (bool, error)
	projectExists       func() (bool, error)
	loadRestriction     func() (string, error)
	// installationKey signs installation authorizations; empty fails the
	// test.
	installationKey string
	// verifyInstallation, when set, is what VerifyInstallation returns in
	// place of checking the signature.
	verifyInstallation error
	// credentials is the transaction the executor credential With methods
	// apply in, with locked.
	credentials *fakeTx
	// deletion is the transaction WithSessionDeletion applies in, with
	// locked.
	deletion       *fakeTx
	updateMetadata func(encoded string) (Session, error)
	auditOperation func() error
	// inputTx is the transaction WithInputs applies in.
	inputTx *fakeInputTx
	// creation is the transaction WithCreation applies in.
	creation     *fakeCreationTx
	fingerprint  func(secret string) (string, error)
	findCreation func() (CreationRecord, error)
}

func (s *fakeStorage) record(name string, set bool, detail ...string) {
	s.t.Helper()
	if !set {
		s.t.Fatalf("unexpected call to %s", name)
	}
	s.calls = append(s.calls, strings.Join(append([]string{name}, detail...), " "))
}

func (s *fakeStorage) TouchDevice(_ context.Context, device string) (bool, error) {
	s.record("TouchDevice", s.touchDevice != nil, device)
	return s.touchDevice()
}

func (s *fakeStorage) TouchAuthenticatedDevice(_ context.Context, device, credentialHash string) (bool, error) {
	s.record("TouchAuthenticatedDevice", s.touchAuthenticatedDevice != nil, device, credentialHash)
	return s.touchAuthenticatedDevice()
}

func (s *fakeStorage) WithEnrollment(ctx context.Context, environment, credentialHash string, apply func(context.Context, EnrollmentTx, Environment, LockedSession) error) error {
	s.record("WithEnrollment", s.enrollment != nil, environment, credentialHash)
	return apply(ctx, s.enrollment, s.environment, s.locked)
}

func (s *fakeStorage) WithSessionDeletion(ctx context.Context, tenant, session string, apply func(context.Context, LockedSession, SessionDeletionTx) error) error {
	s.record("WithSessionDeletion", s.deletion != nil, tenant, session)
	return apply(ctx, s.locked, s.deletion)
}

func (s *fakeStorage) UpdateSessionMetadata(_ context.Context, tenant, session string, encoded []byte) (Session, error) {
	s.record("UpdateSessionMetadata", s.updateMetadata != nil, tenant, session, string(encoded))
	return s.updateMetadata(string(encoded))
}

func (s *fakeStorage) AuditSessionOperation(_ context.Context, tenant, session, action string) error {
	s.record("AuditSessionOperation", s.auditOperation != nil, tenant, session, action)
	return s.auditOperation()
}

func (s *fakeStorage) WithArtifactStaging(context.Context, ArtifactStagingKey, func(context.Context, ArtifactStagingTx) error) error {
	s.t.Fatal("unexpected call to WithArtifactStaging")
	return nil
}

func (s *fakeStorage) DeleteSessionArtifact(context.Context, string, string, string) error {
	s.t.Fatal("unexpected call to DeleteSessionArtifact")
	return nil
}

func deviceService(t *testing.T, storage *fakeStorage) *Service {
	t.Helper()
	service, err := NewService(storage, nil)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

const credentialDigest = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

func TestDeviceUseCases(t *testing.T) {
	// A heartbeat reports whether the device, or the credential it was
	// authenticated with, still has authority.
	for _, current := range []bool{true, false} {
		storage := &fakeStorage{t: t, touchDevice: returns(current), touchAuthenticatedDevice: returns(current)}
		service := deviceService(t, storage)
		runtime, err := service.TouchRuntimeHeartbeat(t.Context(), "device")
		if err != nil || runtime != (runtimedevice.HeartbeatStatus{Liveness: "online", Deleted: !current}) {
			t.Fatalf("runtime heartbeat %+v, %v", runtime, err)
		}
		daemon, err := service.TouchAgentDaemonHeartbeat(t.Context(), runtimedevice.Heartbeat{RuntimeID: "device", CredentialHash: credentialDigest})
		if err != nil || daemon != runtime {
			t.Fatalf("daemon heartbeat %+v, %v", daemon, err)
		}
		if want := []string{"TouchDevice device", "TouchAuthenticatedDevice device " + credentialDigest}; strings.Join(storage.calls, "\n") != strings.Join(want, "\n") {
			t.Fatalf("calls %q", storage.calls)
		}
	}
	failing := func() (bool, error) { return true, errStorage }
	storage := &fakeStorage{t: t, touchDevice: failing, touchAuthenticatedDevice: failing}
	if status, err := deviceService(t, storage).TouchRuntimeHeartbeat(t.Context(), "device"); !errors.Is(err, errStorage) || status != (runtimedevice.HeartbeatStatus{}) {
		t.Fatalf("failed runtime heartbeat %+v, %v", status, err)
	}
	if status, err := deviceService(t, storage).TouchAgentDaemonHeartbeat(t.Context(), runtimedevice.Heartbeat{RuntimeID: "device"}); !errors.Is(err, errStorage) || status != (runtimedevice.HeartbeatStatus{}) {
		t.Fatalf("failed daemon heartbeat %+v, %v", status, err)
	}
}

func TestEnrollRuntimeEnrollsUnderTheSessionLock(t *testing.T) {
	environment := Environment{ID: "environment", SessionID: "session"}
	authorized := returns("key")
	resource := sandboxbootstrap.Resource{TenantID: "tenant", EnvironmentID: "environment", Kind: "enrollment", ID: "enrollment", Generation: 1}
	for _, test := range []struct {
		name   string
		tx     fakeTx
		locked LockedSession
		want   error
		calls  []string
	}{
		{"enrolls", fakeTx{authorizeEnrollment: authorized, enrollSandbox: returns(resource)},
			LockedSession{}, nil, []string{"AuthorizeEnrollment", "EnrollSandbox key"}},
		{"deleted Session", fakeTx{}, LockedSession{Deleted: true}, ErrNotFound, nil},
		{"revoked key", fakeTx{authorizeEnrollment: func() (string, error) { return "", ErrNotFound }},
			LockedSession{}, ErrNotFound, []string{"AuthorizeEnrollment"}},
		{"enrollment of another key", fakeTx{authorizeEnrollment: authorized, enrollSandbox: func() (sandboxbootstrap.Resource, error) {
			return sandboxbootstrap.Resource{}, ErrDeviceBindingConflict
		}},
			LockedSession{}, ErrDeviceBindingConflict, []string{"AuthorizeEnrollment", "EnrollSandbox key"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := test.tx
			tx.t = t
			storage := &fakeStorage{t: t, enrollment: &tx, environment: environment, locked: test.locked}
			enrolled, err := deviceService(t, storage).EnrollRuntime(t.Context(), "environment", credentialDigest)
			if test.want == nil && err != nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if test.want == nil && enrolled != resource {
				t.Fatalf("enrollment %+v", enrolled)
			}
			if test.want != nil && enrolled != (sandboxbootstrap.Resource{}) {
				t.Fatalf("failed enrollment returned %+v", enrolled)
			}
			if storage.calls[0] != "WithEnrollment environment "+credentialDigest {
				t.Fatalf("storage calls %q", storage.calls)
			}
			assertCalls(t, &tx, test.calls...)
		})
	}
}
