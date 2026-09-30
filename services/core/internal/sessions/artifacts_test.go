package sessions

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
)

// exportFile is one entry of a test export.
type exportFile struct {
	name string
	body string
	kind byte
}

// export builds a tar export of files in order, followed by padding.
func export(t *testing.T, padding []byte, files ...exportFile) []byte {
	t.Helper()
	var data bytes.Buffer
	w := tar.NewWriter(&data)
	for _, file := range files {
		kind := file.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: file.name, Mode: 0600, Typeflag: kind}
		if kind == tar.TypeReg {
			header.Size = int64(len(file.body))
		}
		if kind == tar.TypeSymlink {
			header.Linkname = "/etc/passwd"
		}
		if err := w.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(file.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return append(data.Bytes(), padding...)
}

// oversized is an export whose only header declares size bytes; the
// declared content never follows.
func oversized(t *testing.T, size int64) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := tar.NewWriter(&data).WriteHeader(&tar.Header{Name: "outputs/big", Mode: 0600, Size: size, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

type failingRead struct{}

func (failingRead) Read([]byte) (int, error) { return 0, errors.New("transport failed") }

func TestReadArtifactExport(t *testing.T) {
	var put []string
	valid := export(t, make([]byte, maxExportPadding), exportFile{name: "outputs/a.txt", body: "alpha"}, exportFile{name: "outputs/nested/empty"})
	if err := readArtifactExport(bytes.NewReader(valid), func(path string, size int64, content io.Reader) error {
		body, err := io.ReadAll(io.LimitReader(content, size))
		put = append(put, fmt.Sprintf("%s %d %s", path, size, body))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"/workspace/outputs/a.txt 5 alpha", "/workspace/outputs/nested/empty 0 "}; strings.Join(put, "|") != strings.Join(want, "|") {
		t.Fatalf("put %q, want %q", put, want)
	}

	many := make([]exportFile, maxArtifactFiles+1)
	for i := range many {
		many[i] = exportFile{name: fmt.Sprintf("outputs/%d", i)}
	}
	for name, body := range map[string][]byte{
		"directory":        export(t, nil, exportFile{name: "outputs/dir/", kind: tar.TypeDir}),
		"symlink":          export(t, nil, exportFile{name: "outputs/link", kind: tar.TypeSymlink}),
		"private root":     export(t, nil, exportFile{name: "secrets/key", body: "no"}),
		"traversal":        export(t, nil, exportFile{name: "outputs/../secret", body: "no"}),
		"backslash":        export(t, nil, exportFile{name: `outputs/a\b`, body: "no"}),
		"newline":          export(t, nil, exportFile{name: "outputs/a\nb", body: "no"}),
		"long name":        export(t, nil, exportFile{name: "outputs/" + strings.Repeat("a", maxArtifactNameBytes), body: "no"}),
		"duplicate":        export(t, nil, exportFile{name: "outputs/a", body: "1"}, exportFile{name: "outputs/a", body: "2"}),
		"too many files":   export(t, nil, many...),
		"oversized file":   oversized(t, maxArtifactBytes+1),
		"trailing data":    export(t, []byte("not archive padding"), exportFile{name: "outputs/a", body: "data"}),
		"excess padding":   export(t, make([]byte, maxExportPadding+1), exportFile{name: "outputs/a", body: "data"}),
		"nonzero padding":  export(t, append(make([]byte, 10), 1), exportFile{name: "outputs/a", body: "data"}),
		"unterminated tar": export(t, nil, exportFile{name: "outputs/a", body: "data"})[:512],
	} {
		t.Run(name, func(t *testing.T) {
			err := readArtifactExport(bytes.NewReader(body), func(_ string, size int64, content io.Reader) error {
				_, err := io.CopyN(io.Discard, content, size)
				return err
			})
			if err == nil {
				t.Fatal("export accepted")
			}
			if name != "unterminated tar" && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("got %v, want ErrInvalidInput", err)
			}
		})
	}

	// A failed transfer after a valid archive returns its own error.
	transport := io.MultiReader(bytes.NewReader(export(t, nil, exportFile{name: "outputs/a", body: "data"})), failingRead{})
	if err := readArtifactExport(transport, func(_ string, size int64, content io.Reader) error {
		_, err := io.CopyN(io.Discard, content, size)
		return err
	}); err == nil || errors.Is(err, ErrInvalidInput) {
		t.Fatalf("transport failure: %v", err)
	}
	// put's error stops the export.
	if err := readArtifactExport(bytes.NewReader(valid), func(string, int64, io.Reader) error { return errStorage }); !errors.Is(err, errStorage) {
		t.Fatalf("put failure: %v", err)
	}
}

// fakeStaging is a strict artifact staging transaction. Each method records its
// call, then runs its func; a method whose func is unset fails the test.
type fakeStaging struct {
	t     *testing.T
	calls []string

	loadEnvironment func() (Environment, error)
	putContent      func(io.Reader, int64) error
	lockSession     func() (LockedSession, error)
	loadTurn        func() (Turn, error)
	stageArtifacts  func() error
}

func (f *fakeStaging) record(name string, set bool, detail ...string) {
	f.t.Helper()
	if !set {
		f.t.Fatalf("unexpected call to %s", name)
	}
	f.calls = append(f.calls, strings.Join(append([]string{name}, detail...), " "))
}

func (f *fakeStaging) LoadEnvironment(context.Context) (Environment, error) {
	f.record("LoadEnvironment", f.loadEnvironment != nil)
	return f.loadEnvironment()
}

func (f *fakeStaging) PutArtifactContent(_ context.Context, path string, size int64, content io.Reader) error {
	f.record("PutArtifactContent", f.putContent != nil, path)
	return f.putContent(content, size)
}

func (f *fakeStaging) LockSession(context.Context) (LockedSession, error) {
	f.record("LockSession", f.lockSession != nil)
	return f.lockSession()
}

func (f *fakeStaging) LoadTurn(context.Context) (Turn, error) {
	f.record("LoadTurn", f.loadTurn != nil)
	return f.loadTurn()
}

func (f *fakeStaging) StageArtifacts(context.Context) error {
	f.record("StageArtifacts", f.stageArtifacts != nil)
	return f.stageArtifacts()
}

// fakeArtifactStorage runs each staging in tx and records the key; any other
// call fails the test.
type fakeArtifactStorage struct {
	t    *testing.T
	tx   *fakeStaging
	keys []ArtifactStagingKey
}

func (s *fakeArtifactStorage) WithArtifactStaging(ctx context.Context, key ArtifactStagingKey, stage func(context.Context, ArtifactStagingTx) error) error {
	s.keys = append(s.keys, key)
	return stage(ctx, s.tx)
}

func (s *fakeArtifactStorage) DeleteSessionArtifact(context.Context, string, string, string) error {
	s.t.Fatal("unexpected call to DeleteSessionArtifact")
	return nil
}

func (s *fakeArtifactStorage) CreateDevice(context.Context, string, DeviceRegistration) (ExecutionDevice, error) {
	s.t.Fatal("unexpected call to CreateDevice")
	return ExecutionDevice{}, nil
}

func (s *fakeArtifactStorage) RevokeDevice(context.Context, string, string) error {
	s.t.Fatal("unexpected call to RevokeDevice")
	return nil
}

func (s *fakeArtifactStorage) TouchDevice(context.Context, string) (bool, error) {
	s.t.Fatal("unexpected call to TouchDevice")
	return false, nil
}

func (s *fakeArtifactStorage) TouchAuthenticatedDevice(context.Context, string, string) (bool, error) {
	s.t.Fatal("unexpected call to TouchAuthenticatedDevice")
	return false, nil
}

func (s *fakeArtifactStorage) WithEnrollment(context.Context, string, string, func(context.Context, EnrollmentTx, Environment, LockedSession) error) error {
	s.t.Fatal("unexpected call to WithEnrollment")
	return nil
}

func (s *fakeArtifactStorage) GetEnvironment(context.Context, string, string) (Environment, error) {
	s.t.Fatal("unexpected call to GetEnvironment")
	return Environment{}, nil
}

func (s *fakeArtifactStorage) LoadProjectArchived(context.Context, string) (bool, error) {
	s.t.Fatal("unexpected call to LoadProjectArchived")
	return false, nil
}

func (s *fakeArtifactStorage) ExecutorProjectExists(context.Context, identity.ProjectScope) (bool, error) {
	s.t.Fatal("unexpected call to ExecutorProjectExists")
	return false, nil
}

func (s *fakeArtifactStorage) LoadExecutorCredentialRestriction(context.Context, identity.Principal, string) (string, error) {
	s.t.Fatal("unexpected call to LoadExecutorCredentialRestriction")
	return "", nil
}

func (s *fakeArtifactStorage) SignInstallation(context.Context, string) (string, error) {
	s.t.Fatal("unexpected call to SignInstallation")
	return "", nil
}

func (s *fakeArtifactStorage) VerifyInstallation(context.Context, string, string) error {
	s.t.Fatal("unexpected call to VerifyInstallation")
	return nil
}

func (s *fakeArtifactStorage) WithExecutorCredentials(context.Context, string, func(context.Context, ExecutorCredentialTx) error) error {
	s.t.Fatal("unexpected call to WithExecutorCredentials")
	return nil
}

func (s *fakeArtifactStorage) WithEnvironmentExecutorCredentials(context.Context, string, string, func(context.Context, EnvironmentExecutorCredentialTx, LockedSession) error) error {
	s.t.Fatal("unexpected call to WithEnvironmentExecutorCredentials")
	return nil
}

func discard(content io.Reader, size int64) error {
	_, err := io.CopyN(io.Discard, content, size)
	return err
}

func TestStageTurnArtifacts(t *testing.T) {
	hosted := Environment{ID: "environment", Configuration: []byte(`{"type":"openai_hosted"}`)}
	running := Turn{ID: "turn", Status: TurnInProgress}
	command := func(body []byte) StageTurnArtifactsCommand {
		return StageTurnArtifactsCommand{TenantID: "tenant", SessionID: "session", TurnID: "turn", EnvironmentID: "environment", Export: bytes.NewReader(body)}
	}
	valid := export(t, nil, exportFile{name: "outputs/a", body: "alpha"}, exportFile{name: "outputs/b", body: "bravo"})
	transfer := []string{"LoadEnvironment", "PutArtifactContent /workspace/outputs/a", "PutArtifactContent /workspace/outputs/b", "LockSession", "LoadTurn"}
	for _, test := range []struct {
		name  string
		tx    fakeStaging
		body  []byte
		want  error
		calls []string
	}{
		{"stages after the lock", fakeStaging{loadEnvironment: returns(hosted), putContent: discard, lockSession: returns(LockedSession{}), loadTurn: returns(running), stageArtifacts: done}, valid, nil, append(transfer, "StageArtifacts")},
		{"another Environment", fakeStaging{loadEnvironment: returns(Environment{ID: "other", Configuration: hosted.Configuration})}, valid, ErrNotFound, []string{"LoadEnvironment"}},
		{"no captured Environment type", fakeStaging{loadEnvironment: returns(Environment{ID: "environment", Configuration: []byte(`{"type":"none"}`)})}, valid, ErrInvalidInput, []string{"LoadEnvironment"}},
		{"invalid export", fakeStaging{loadEnvironment: returns(hosted), putContent: discard}, export(t, nil, exportFile{name: "outputs/a", body: "alpha"}, exportFile{name: "secrets/key", body: "no"}), ErrInvalidInput, []string{"LoadEnvironment", "PutArtifactContent /workspace/outputs/a"}},
		{"deleted Session", fakeStaging{loadEnvironment: returns(hosted), putContent: discard, lockSession: returns(LockedSession{Deleted: true})}, valid, ErrNotFound, transfer[:4]},
		{"settled Turn", fakeStaging{loadEnvironment: returns(hosted), putContent: discard, lockSession: returns(LockedSession{}), loadTurn: returns(Turn{ID: "turn", Status: TurnCompleted})}, valid, ErrTurnConflict, transfer},
		{"cancel requested", fakeStaging{loadEnvironment: returns(hosted), putContent: discard, lockSession: returns(LockedSession{}), loadTurn: returns(Turn{ID: "turn", Status: TurnInProgress, CancelRequestedAt: time.Unix(1, 0)})}, valid, ErrTurnConflict, transfer},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := test.tx
			tx.t = t
			storage := &fakeArtifactStorage{t: t, tx: &tx}
			service, err := NewService(storage)
			if err != nil {
				t.Fatal(err)
			}
			err = service.StageTurnArtifacts(t.Context(), command(test.body))
			if test.want == nil && err != nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if strings.Join(tx.calls, "\n") != strings.Join(test.calls, "\n") {
				t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(tx.calls, "\n"), strings.Join(test.calls, "\n"))
			}
			if want := (ArtifactStagingKey{TenantID: "tenant", SessionID: "session", TurnID: "turn", EnvironmentID: "environment"}); len(storage.keys) != 1 || storage.keys[0] != want {
				t.Fatalf("staging keys %+v", storage.keys)
			}
		})
	}

	// Without an export nothing reaches storage.
	service, err := NewService(&fakeArtifactStorage{t: t})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StageTurnArtifacts(t.Context(), StageTurnArtifactsCommand{TenantID: "tenant", SessionID: "session", TurnID: "turn", EnvironmentID: "environment"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
}

func TestNewServiceRequiresStorage(t *testing.T) {
	if _, err := NewService(nil); err == nil {
		t.Fatal("service built without storage")
	}
}
