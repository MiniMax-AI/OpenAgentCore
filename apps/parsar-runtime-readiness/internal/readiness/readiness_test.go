package readiness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// fixture is a temporary Runtime home plus a stubbed liveness probe, so every
// branch is exercised without a sandbox.
type fixture struct {
	environment Environment
	home        string
	profile     string
	alive       map[int]bool
	now         time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	root := t.TempDir()
	layout := []string{filepath.Join(root, "workspace"), filepath.Join(root, "staging")}
	for _, path := range layout {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := &fixture{home: home, profile: filepath.Join(home, "parsar-daemon", Profile), alive: map[int]bool{}, now: time.Unix(2000000000, 0).UTC()}
	f.environment = Environment{
		Home:   home,
		Layout: layout,
		Alive:  func(pid int) bool { return f.alive[pid] },
		Now:    func() time.Time { return f.now },
	}
	return f
}

func (f *fixture) write(t *testing.T, name, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(f.profile, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.profile, name)
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// statement renders the daemon's published state.
func (f *fixture) statement(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	body := map[string]any{"version": 1, "connected": true, "device_id": "device-1", "pid": 4242, "updated_at": f.now.Add(-time.Second)}
	mutate(body)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// requireUnixFileModes documents that the profile mode contract is a Unix file
// mode: Windows reports 0666 or 0444 for any file, so these assertions only mean
// something in the Linux sandbox where the endpoint actually runs.
func requireUnixFileModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the readiness profile mode contract is a Unix file mode")
	}
}

// The template build has no daemon profile, so image readiness is the whole
// contract there. Every other state is reported honestly.
func TestEvaluateReadinessStates(t *testing.T) {
	requireUnixFileModes(t)
	f := newFixture(t)
	auth := `{"server_url":"http://core.invalid/api/v1","runtime_id":"device-1","runner_credential":"synthetic"}`
	if state := f.environment.Evaluate(); !state.Ready || state.State != StateUnprovisioned {
		t.Fatalf("unprovisioned image misreported: %+v", state)
	}
	// A provisioned Runtime whose profile file is gone is not unprovisioned: the
	// profile directory is the provisioned signal.
	if err := os.MkdirAll(f.profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if state := f.environment.Evaluate(); state.Ready || state.State != StateWaitingForDaemon {
		t.Fatalf("a lost profile was reported as an unprovisioned image: %+v", state)
	}
	f.write(t, "auth.json", auth, 0o600)
	if state := f.environment.Evaluate(); state.Ready || state.State != StateWaitingForDaemon {
		t.Fatalf("profile without a connection misreported: %+v", state)
	}
	f.write(t, "connect.state", f.statement(t, func(map[string]any) {}), 0o600)
	if state := f.environment.Evaluate(); state.Ready || state.State != StateWaitingForDaemon {
		t.Fatalf("a statement whose process is gone was trusted: %+v", state)
	}
	f.alive[4242] = true
	if state := f.environment.Evaluate(); !state.Ready || state.State != StateConnected {
		t.Fatalf("connected Runtime misreported: %+v", state)
	}
}

func TestEvaluateRejectsWeakerEvidence(t *testing.T) {
	requireUnixFileModes(t)
	auth := `{"server_url":"http://core.invalid/api/v1","runtime_id":"device-1","runner_credential":"synthetic"}`
	for name, prepare := range map[string]func(*fixture, *testing.T){
		"auth profile is world readable": func(f *fixture, t *testing.T) {
			f.write(t, "auth.json", auth, 0o644)
			f.write(t, "connect.state", f.statement(t, func(map[string]any) {}), 0o600)
			f.alive[4242] = true
		},
		"statement is stale": func(f *fixture, t *testing.T) {
			f.write(t, "auth.json", auth, 0o600)
			f.write(t, "connect.state", f.statement(t, func(body map[string]any) { body["updated_at"] = f.now.Add(-48 * time.Hour) }), 0o600)
			f.alive[4242] = true
		},
		"statement has an unknown version": func(f *fixture, t *testing.T) {
			f.write(t, "auth.json", auth, 0o600)
			f.write(t, "connect.state", f.statement(t, func(body map[string]any) { body["version"] = 2 }), 0o600)
			f.alive[4242] = true
		},
		"statement withdraws the connection": func(f *fixture, t *testing.T) {
			f.write(t, "auth.json", auth, 0o600)
			f.write(t, "connect.state", f.statement(t, func(body map[string]any) { body["connected"] = false }), 0o600)
			f.alive[4242] = true
		},
		"statement has no device": func(f *fixture, t *testing.T) {
			f.write(t, "auth.json", auth, 0o600)
			f.write(t, "connect.state", f.statement(t, func(body map[string]any) { body["device_id"] = " " }), 0o600)
			f.alive[4242] = true
		},
		"statement is not JSON": func(f *fixture, t *testing.T) {
			f.write(t, "auth.json", auth, 0o600)
			f.write(t, "connect.state", "not-json", 0o600)
			f.alive[4242] = true
		},
		"statement has no pid": func(f *fixture, t *testing.T) {
			f.write(t, "auth.json", auth, 0o600)
			f.write(t, "connect.state", f.statement(t, func(body map[string]any) { body["pid"] = 0 }), 0o600)
			f.alive[4242] = true
		},
	} {
		f := newFixture(t)
		prepare(f, t)
		if state := f.environment.Evaluate(); state.Ready || state.State != StateWaitingForDaemon {
			t.Fatalf("%s: reported as ready (%+v)", name, state)
		}
	}
}

func TestEvaluateRequiresTheQualifiedImageLayout(t *testing.T) {
	f := newFixture(t)
	if state := f.environment.Evaluate(); !state.Ready {
		t.Fatalf("qualified image misreported: %+v", state)
	}
	if err := os.RemoveAll(f.environment.Layout[1]); err != nil {
		t.Fatal(err)
	}
	if state := f.environment.Evaluate(); state.Ready || state.State != StateIncomplete {
		t.Fatalf("incomplete image misreported: %+v", state)
	}
}

// The template probe contract is a fixed package constant: the image, the
// template and the adapter must agree on it.
func TestDefaultUsesTheDocumentedPortAndPaths(t *testing.T) {
	if Port != 49984 || Path != "/healthz" {
		t.Fatalf("the Cube template probe contract changed: %d %s", Port, Path)
	}
	t.Setenv("PARSAR_HOME", "/tmp/runtime-home")
	environment := Default()
	if environment.Home != "/tmp/runtime-home" {
		t.Fatalf("documented Runtime home lost: %q", environment.Home)
	}
	want := []string{"/environment/workspace", "/environment/staging", "/environment/initialization", "/environment/packages"}
	if len(environment.Layout) != len(want) {
		t.Fatalf("layout lost: %v", environment.Layout)
	}
	for index, path := range want {
		if environment.Layout[index] != path {
			t.Fatalf("layout lost: %v", environment.Layout)
		}
		if filepath.Base(path) == Profile {
			t.Fatalf("the profile directory must not be part of the image layout: %s", path)
		}
	}
}
