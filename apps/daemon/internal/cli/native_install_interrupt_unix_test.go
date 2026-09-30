//go:build unix

package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNativeInstallerInterruptHelper(t *testing.T) {
	if os.Getenv("OAC_TEST_INSTALL_INTERRUPT") != "1" {
		t.Skip("subprocess helper")
	}
	var args []string
	if json.Unmarshal([]byte(os.Getenv("OAC_TEST_INSTALL_ARGS")), &args) != nil {
		os.Exit(3)
	}
	err := runInstall(&runContext{stdout: io.Discard, stderr: io.Discard}, args)
	if err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestNativeInstallationInterruptReapsProbe(t *testing.T) {
	_, args, root, bundle := nativeInstallFixture(t)
	marker := filepath.Join(t.TempDir(), "probe-pids")
	var b nativeBundle
	if err := readNativeJSON(filepath.Join(bundle, "bundle.json"), &b); err != nil {
		t.Fatal(err)
	}
	// A real, separately grouped subprocess must be reaped by installation's
	// signal handler, not just by a caller returning a synthetic probe error.
	script := []byte("#!/bin/sh\nsleep 300 &\nprintf '%s %s' \"$$\" \"$!\" > \"$OAC_TEST_PROBE_PIDS\"\nwait\n")
	sum := sha256.Sum256(script)
	b.Components["node"] = nativeComponent{Version: nativePins["node"], Files: map[string]nativeFile{"bin/node": {SHA256: hex.EncodeToString(sum[:]), Executable: true}}}
	dir := filepath.Join(nativeComponentRoot(bundle, "node"), "bin")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node"), script, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	if err := os.WriteFile(filepath.Join(bundle, "bundle.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	arguments, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^TestNativeInstallerInterruptHelper$")
	cmd.Env = append(os.Environ(), "OAC_TEST_INSTALL_INTERRUPT=1", "OAC_TEST_INSTALL_ARGS="+string(arguments), "OAC_TEST_PROBE_PIDS="+marker)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() { _ = cmd.Process.Kill() }()
	var pids []string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil {
			pids = strings.Fields(string(data))
			if len(pids) == 2 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(pids) != 2 {
		t.Fatal("probe did not start")
	}
	defer func() {
		for _, id := range pids {
			pid, _ := strconv.Atoi(id)
			p, _ := os.FindProcess(pid)
			_ = p.Kill()
		}
	}()
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("interrupted installation succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("installer did not settle after interrupt")
	}
	for _, pid := range pids {
		state, _ := exec.Command("ps", "-p", pid, "-o", "stat=").Output()
		if s := strings.TrimSpace(string(state)); s != "" && !strings.HasPrefix(s, "Z") {
			t.Fatal("interrupted installer left a running native process")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "daemon", "installation.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("interrupted installation committed settings")
	}
}
