package daemonize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConnectedStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connect.state")
	if err := RemoveConnectedState(path); err != nil {
		t.Fatalf("withdrawing an absent statement failed: %v", err)
	}
	if err := WriteConnectedState(path, "device-1"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("connected state is not private: %o", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state ConnectedState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if !state.Connected || state.Version != connectedStateVersion || state.DeviceID != "device-1" || state.PID != os.Getpid() || state.UpdatedAt.IsZero() {
		t.Fatalf("connected state lost its contract: %+v", state)
	}
	if err := RemoveConnectedState(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("withdrawn statement still present: %v", err)
	}
	if err := WriteConnectedState("", "device-1"); err == nil {
		t.Fatal("an empty path was accepted")
	}
}
