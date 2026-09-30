package daemonize

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessRecordIdentity(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "connect.pid")
	if err := WritePIDFile(path, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	pid, err := ReadPIDFile(path)
	if err != nil || pid != os.Getpid() {
		t.Fatal(pid, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var identity processIdentity
	if json.Unmarshal(raw, &identity) != nil || identity.Start == "" {
		t.Fatal("missing process start identity")
	}
	identity.Start += "different"
	raw, _ = json.Marshal(identity)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = StopPIDFile(path, time.Second); !errors.Is(err, ErrStaleOrCorrupt) {
		t.Fatal("stale identity was accepted", err)
	}
	if err = IsAlive(os.Getpid()); err != nil {
		t.Fatal("unrelated process was affected", err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("unconfirmed record removed", err)
	}
}

func TestProcessRecordRejectsOldAndMalformed(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "connect.pid")
	if _, err := ReadPIDFile(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, raw := range []string{"123\n", "{}", "not-json"} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPIDFile(path); !errors.Is(err, ErrStaleOrCorrupt) {
			t.Fatal(raw, err)
		}
	}
	if err := RemovePIDFile(path); err != nil {
		t.Fatal(err)
	}
	if err := RemovePIDFile(path); err != nil {
		t.Fatal(err)
	}
}

func TestStopTimeoutRetainsProcessRecord(t *testing.T) {
	dir := privateTempDir(t)
	path := filepath.Join(dir, "connect.pid")
	pid, err := Spawn([]string{"daemon", "child"}, ReExecOptions{LogPath: filepath.Join(dir, "log"), PIDPath: path, ExtraEnv: []string{spawnTestChildEnv + "=ignore"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		p, _ := os.FindProcess(pid)
		if p != nil {
			_ = p.Kill()
			_, _ = p.Wait()
		}
	}()
	// Unix has no readiness event; wait until the child installs its signal handler.
	deadline := time.Now().Add(3 * time.Second)
	for {
		raw, _ := os.ReadFile(filepath.Join(dir, "log"))
		if len(raw) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = StopPIDFile(path, 100*time.Millisecond); err == nil {
		t.Fatal("unconfirmed stop succeeded")
	}
	if got, err := ReadPIDFile(path); err != nil || got != pid {
		t.Fatal("timeout lost ownership", got, err)
	}
}
