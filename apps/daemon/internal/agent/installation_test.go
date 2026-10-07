package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestManifestEnvironmentActivatesEachListedInstallation(t *testing.T) {
	fake := Installation{AgentKind: "fake", Environment: func(dir, node string) map[string]string {
		return map[string]string{"FAKE_DIR": dir, "FAKE_NODE": node}
	}}
	for manifest, want := range map[string]map[string]string{
		`{"node":"/n/node","harnesses":{"fake":"/h/fake"}}`:      {"FAKE_DIR": "/h/fake", "FAKE_NODE": "/n/node"},
		`{"node":"/n/node","harnesses":{"other":"/h/other"}}`:    nil,
		`{"node":"/n/node","harnesses":{"fake":"h/fake"}}`:       nil,
		`{"node":"node","harnesses":{"fake":"/h/fake"}}`:         nil,
		`{"node":"/n/node","harnesses":{},"fake_dir":"/h/fake"}`: nil,
	} {
		path := filepath.Join(t.TempDir(), "harnesses.json")
		if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := ManifestEnvironment(path, fake)
		if want == nil && err == nil || want != nil && (err != nil || !reflect.DeepEqual(got, want)) {
			t.Errorf("ManifestEnvironment(%s) = %v, %v; want %v", manifest, got, err, want)
		}
	}
}
