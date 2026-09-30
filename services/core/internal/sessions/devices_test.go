package sessions

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
)

// fakeDeviceStorage is strict pooled Session storage for the device use
// cases. Each method records its call, with the arguments that identify it,
// then runs its func; a method whose func is unset fails the test.
type fakeDeviceStorage struct {
	t     *testing.T
	calls []string

	createDevice             func(DeviceRegistration) (ExecutionDevice, error)
	revokeDevice             func() error
	touchDevice              func() (bool, error)
	touchAuthenticatedDevice func() (bool, error)
	// enrollment is the transaction WithEnrollment applies in, with
	// environment and locked.
	enrollment  *fakeTx
	environment Environment
	locked      LockedSession
}

func (s *fakeDeviceStorage) record(name string, set bool, detail ...string) {
	s.t.Helper()
	if !set {
		s.t.Fatalf("unexpected call to %s", name)
	}
	s.calls = append(s.calls, strings.Join(append([]string{name}, detail...), " "))
}

func (s *fakeDeviceStorage) CreateDevice(_ context.Context, tenant string, registration DeviceRegistration) (ExecutionDevice, error) {
	s.record("CreateDevice", s.createDevice != nil, tenant, registration.Name, registration.CredentialHash)
	return s.createDevice(registration)
}

func (s *fakeDeviceStorage) RevokeDevice(_ context.Context, tenant, device string) error {
	s.record("RevokeDevice", s.revokeDevice != nil, tenant, device)
	return s.revokeDevice()
}

func (s *fakeDeviceStorage) TouchDevice(_ context.Context, device string) (bool, error) {
	s.record("TouchDevice", s.touchDevice != nil, device)
	return s.touchDevice()
}

func (s *fakeDeviceStorage) TouchAuthenticatedDevice(_ context.Context, device, credentialHash string) (bool, error) {
	s.record("TouchAuthenticatedDevice", s.touchAuthenticatedDevice != nil, device, credentialHash)
	return s.touchAuthenticatedDevice()
}

func (s *fakeDeviceStorage) WithEnrollment(ctx context.Context, environment, credentialHash string, apply func(context.Context, EnrollmentTx, Environment, LockedSession) error) error {
	s.record("WithEnrollment", s.enrollment != nil, environment, credentialHash)
	return apply(ctx, s.enrollment, s.environment, s.locked)
}

func (s *fakeDeviceStorage) WithArtifactStaging(context.Context, ArtifactStagingKey, func(context.Context, ArtifactStagingTx) error) error {
	s.t.Fatal("unexpected call to WithArtifactStaging")
	return nil
}

func (s *fakeDeviceStorage) DeleteSessionArtifact(context.Context, string, string, string) error {
	s.t.Fatal("unexpected call to DeleteSessionArtifact")
	return nil
}

func deviceService(t *testing.T, storage *fakeDeviceStorage) *Service {
	t.Helper()
	service, err := NewService(storage)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

const credentialDigest = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

func TestNewDeviceRegistration(t *testing.T) {
	registration, err := NewDeviceRegistration("  runtime  ", strings.ToUpper(credentialDigest))
	if err != nil || registration != (DeviceRegistration{Name: "runtime", CredentialHash: credentialDigest}) {
		t.Fatalf("registration %+v, %v", registration, err)
	}
	if _, err := NewDeviceRegistration(strings.Repeat("n", 256), credentialDigest); err != nil {
		t.Fatalf("longest name: %v", err)
	}
	for name, input := range map[string][2]string{
		"blank name":      {"   ", credentialDigest},
		"long name":       {strings.Repeat("n", 257), credentialDigest},
		"not hex":         {"runtime", "not-a-digest"},
		"short digest":    {"runtime", credentialDigest[:62]},
		"raw credential":  {"runtime", "secret"},
		"missing digest":  {"runtime", ""},
		"padded digest":   {"runtime", " " + credentialDigest},
		"too long digest": {"runtime", credentialDigest + "00"},
	} {
		if _, err := NewDeviceRegistration(input[0], input[1]); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestDeviceUseCases(t *testing.T) {
	storage := &fakeDeviceStorage{t: t}
	if _, err := deviceService(t, storage).CreateDevice(t.Context(), "tenant", "runtime", "secret"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid registration: %v", err)
	}
	created := ExecutionDevice{ID: "device", Name: "runtime"}
	storage.createDevice = func(DeviceRegistration) (ExecutionDevice, error) { return created, nil }
	storage.revokeDevice = done
	if device, err := deviceService(t, storage).CreateDevice(t.Context(), "tenant", " runtime ", strings.ToUpper(credentialDigest)); err != nil || device != created {
		t.Fatalf("created %+v, %v", device, err)
	}
	if err := deviceService(t, storage).RevokeDevice(t.Context(), "tenant", "device"); err != nil {
		t.Fatal(err)
	}
	want := []string{"CreateDevice tenant runtime " + credentialDigest, "RevokeDevice tenant device"}
	if strings.Join(storage.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls %q", storage.calls)
	}

	// A heartbeat reports whether the device, or the credential it was
	// authenticated with, still has authority.
	for _, current := range []bool{true, false} {
		storage := &fakeDeviceStorage{t: t, touchDevice: returns(current), touchAuthenticatedDevice: returns(current)}
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
	storage = &fakeDeviceStorage{t: t, touchDevice: failing, touchAuthenticatedDevice: failing}
	if status, err := deviceService(t, storage).TouchRuntimeHeartbeat(t.Context(), "device"); !errors.Is(err, errStorage) || status != (runtimedevice.HeartbeatStatus{}) {
		t.Fatalf("failed runtime heartbeat %+v, %v", status, err)
	}
	if status, err := deviceService(t, storage).TouchAgentDaemonHeartbeat(t.Context(), runtimedevice.Heartbeat{RuntimeID: "device"}); !errors.Is(err, errStorage) || status != (runtimedevice.HeartbeatStatus{}) {
		t.Fatalf("failed daemon heartbeat %+v, %v", status, err)
	}
}

func TestEnrollRuntimeBindsUnderTheSessionLock(t *testing.T) {
	environment := Environment{ID: "environment", SessionID: "session"}
	authorized := returns(EnrollmentAuthority{KeyID: "key", WorkspaceDirectory: "/workspace"})
	for _, test := range []struct {
		name   string
		tx     fakeTx
		locked LockedSession
		want   error
		calls  []string
	}{
		{"enrolls", fakeTx{authorizeEnrollment: authorized, enrollDevice: returns("device"), bindDevice: done},
			LockedSession{}, nil, []string{"AuthorizeEnrollment", "EnrollDevice key", "BindDevice device"}},
		{"deleted Session", fakeTx{}, LockedSession{Deleted: true}, ErrNotFound, nil},
		{"revoked key", fakeTx{authorizeEnrollment: func() (EnrollmentAuthority, error) { return EnrollmentAuthority{}, ErrNotFound }},
			LockedSession{}, ErrNotFound, []string{"AuthorizeEnrollment"}},
		{"device of another key", fakeTx{authorizeEnrollment: authorized, enrollDevice: func() (string, error) { return "", ErrDeviceBindingConflict }},
			LockedSession{}, ErrDeviceBindingConflict, []string{"AuthorizeEnrollment", "EnrollDevice key"}},
		{"Session bound elsewhere", fakeTx{authorizeEnrollment: authorized, enrollDevice: returns("device"), bindDevice: func() error { return ErrDeviceBindingConflict }},
			LockedSession{}, ErrDeviceBindingConflict, []string{"AuthorizeEnrollment", "EnrollDevice key", "BindDevice device"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := test.tx
			tx.t = t
			storage := &fakeDeviceStorage{t: t, enrollment: &tx, environment: environment, locked: test.locked}
			enrolled, err := deviceService(t, storage).EnrollRuntime(t.Context(), "environment", credentialDigest)
			if test.want == nil && err != nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if want := (RuntimeEnrollment{DeviceID: "device", SessionID: "session", EnvironmentID: "environment", WorkspaceDirectory: "/workspace"}); test.want == nil && enrolled != want {
				t.Fatalf("enrollment %+v", enrolled)
			}
			if test.want != nil && enrolled != (RuntimeEnrollment{}) {
				t.Fatalf("failed enrollment returned %+v", enrolled)
			}
			if storage.calls[0] != "WithEnrollment environment "+credentialDigest {
				t.Fatalf("storage calls %q", storage.calls)
			}
			assertCalls(t, &tx, test.calls...)
		})
	}
}
