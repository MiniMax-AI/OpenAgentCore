package microsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "microsandbox-test-agent" {
		var q Request
		if json.NewDecoder(os.Stdin).Decode(&q) != nil {
			os.Exit(2)
		}
		_, _ = (&ProcessCaller{LeasePath: q.Config.RuntimeHome, LeaseIdentity: testLeaseIdentity()}).Call(context.Background(), q)
		os.Exit(0)
	}
	if filepath.Base(os.Args[0]) == "microsandbox-test-helper" {
		var q Request
		if json.NewDecoder(os.Stdin).Decode(&q) != nil {
			os.Exit(2)
		}
		switch q.Operation {
		case "lease-held":
			// Model the new native helper's entrypoint before any SDK subprocess.
			if os.Getenv("OAC_NODE_GENERATION_LEASE_FD") != "3" {
				os.Exit(4)
			}
			syscall.CloseOnExec(3)
			if os.WriteFile(q.Config.RuntimePath, []byte("started"), 0600) != nil {
				os.Exit(3)
			}
			until := time.Now().Add(15 * time.Second)
			for {
				if _, err := os.Stat(q.Config.RuntimePath + ".release"); err == nil {
					break
				}
				if time.Now().After(until) {
					os.Exit(5)
				}
				time.Sleep(time.Millisecond)
			}

		case "delayed":
			time.Sleep(150 * time.Millisecond)
			if os.WriteFile(q.Config.RuntimePath, []byte("settled"), 0600) != nil {
				os.Exit(3)
			}
		case "oversize":
			fmt.Print(strings.Repeat("x", MaxResponseBytes+1))
			os.Exit(0)
		case "trailing":
			fmt.Print("{\"Version\":1}{}")
			os.Exit(0)
		}
		_ = json.NewEncoder(os.Stdout).Encode(Response{Version: ProtocolVersion})
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func processConfig(t *testing.T) Config {
	t.Helper()
	c := testConfig()
	directory := t.TempDir()
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	c.HelperPath = filepath.Join(directory, "microsandbox-test-helper")
	if e = os.Symlink(executable, c.HelperPath); e != nil {
		t.Fatal(e)
	}
	c.RuntimePath = filepath.Join(directory, "settled")
	return c
}
func TestResponseTimeoutDoesNotKillLifecycleOwner(t *testing.T) {
	c := processConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, e := (&ProcessCaller{}).Call(ctx, Request{Operation: "delayed", Config: c})
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("want timeout: %v", e)
	}
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		if data, e := os.ReadFile(c.RuntimePath); e == nil && string(data) == "settled" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("helper was killed before settlement")
}
func TestInvalidOrUnboundedHelperOutputIsUnconfirmed(t *testing.T) {
	for _, mode := range []string{"oversize", "trailing"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, e := (&ProcessCaller{}).Call(ctx, Request{Operation: mode, Config: processConfig(t)})
			if e == nil {
				t.Fatal("invalid helper output accepted")
			}
		})
	}
}

func TestGenerationLeaseSurvivesNodeProcessExit(t *testing.T) {
	c := processConfig(t)
	c.RuntimeHome = filepath.Join(filepath.Dir(c.RuntimePath), "1.lease")
	writeTestLease(t, c.RuntimeHome)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(filepath.Dir(c.RuntimePath), "microsandbox-test-agent")
	if err := os.Symlink(executable, agent); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(Request{Operation: "lease-held", Config: c})
	command := exec.Command(agent)
	command.Stdin = strings.NewReader(string(raw))
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	defer func() {
		_ = os.WriteFile(c.RuntimePath+".release", nil, 0600)
		waitProcessCondition(t, func() bool { return exclusiveLease(c.RuntimeHome) == nil })
	}()
	waitProcessCondition(t, func() bool { _, err := os.Stat(c.RuntimePath); return err == nil })
	before, err := os.Stat(c.RuntimeHome)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	// A new node has no in-memory references, but cannot collect the old helper.
	restarted := &ProcessCaller{LeasePath: c.RuntimeHome, LeaseIdentity: testLeaseIdentity()}
	if !restarted.Quiescent() {
		t.Fatal("fresh process unexpectedly has references")
	}
	if err := exclusiveLease(c.RuntimeHome); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("live orphan helper lost lease: %v", err)
	}
	// A fresh node must also refuse an owned replacement regular inode while
	// the actual orphan helper still holds the original open file description.
	if err := os.Rename(c.RuntimeHome, c.RuntimeHome+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.RuntimeHome, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if file, err := restarted.acquireLease(); err == nil {
		file.Close()
		t.Fatal("live helper bypassed through replacement inode")
	}
	if err := exclusiveLease(c.RuntimeHome + ".old"); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatal("original helper lock lost", err)
	}
	if err := os.Remove(c.RuntimeHome); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(c.RuntimeHome+".old", c.RuntimeHome); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.RuntimePath+".release", nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitProcessCondition(t, func() bool { return exclusiveLease(c.RuntimeHome) == nil })
	after, err := os.Stat(c.RuntimeHome)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("lease inode changed", err)
	}
}

func exclusiveLease(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func waitProcessCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("process condition did not settle")
}

func TestGenerationLeaseExcludesCollectionAndDroppedGeneration(t *testing.T) {
	c := processConfig(t)
	lease := filepath.Join(filepath.Dir(c.RuntimePath), "1.lease")
	writeTestLease(t, lease)
	file, err := os.OpenFile(lease, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	caller := &ProcessCaller{LeasePath: lease, LeaseIdentity: testLeaseIdentity()}
	if _, err := caller.Call(t.Context(), Request{Operation: "delayed", Config: c}); err == nil {
		t.Fatal("helper started during exclusive collection")
	}
	if err := os.WriteFile(strings.TrimSuffix(lease, ".lease")+".dropped", []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if _, err := caller.Call(t.Context(), Request{Operation: "delayed", Config: c}); err == nil {
		t.Fatal("helper started after payload collection")
	}
	if _, err := os.Stat(c.RuntimePath); !os.IsNotExist(err) {
		t.Fatal("refused helper mutated files", err)
	}
	// Never accept an alternate inode through symlinks or multiply linked files.
	if err := os.Remove(lease); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(c.RuntimePath, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := caller.acquireLease(); err == nil {
		t.Fatal("symlink lease accepted")
	}
}

func testLeaseIdentity() LeaseIdentity {
	return LeaseIdentity{InstallationID: "test-installation", Generation: 1, SpecificationDigest: (sandbox.DeploymentSpec{}).Digest("microsandbox")}
}

func writeTestLease(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	value := struct {
		LeaseIdentity
		Device uint64 `json:"device"`
		Inode  uint64 `json:"inode"`
	}{testLeaseIdentity(), uint64(st.Dev), st.Ino}
	raw, _ := json.Marshal(value)
	if err := os.WriteFile(strings.TrimSuffix(path, ".lease")+".lease-identity", raw, 0600); err != nil {
		t.Fatal(err)
	}
	writeTestFinalGeneration(t, path)
}

func writeTestFinalGeneration(t *testing.T, path string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"installation_id": "test-installation", "generation": 1, "provider": "microsandbox", "specification": sandbox.DeploymentSpec{}})
	if err := os.WriteFile(strings.TrimSuffix(path, ".lease")+".json", raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationLeaseReplacementRejectedAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "1.lease")
	writeTestLease(t, path)
	caller := &ProcessCaller{LeasePath: path, LeaseIdentity: testLeaseIdentity()}
	old, err := caller.acquireLease()
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	restarted := &ProcessCaller{LeasePath: path, LeaseIdentity: testLeaseIdentity()}
	if file, err := restarted.acquireLease(); err == nil {
		file.Close()
		t.Fatal("replacement inode was adopted")
	}
	if err := exclusiveLease(path + ".old"); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatal("old helper lease lost", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".old", path); err != nil {
		t.Fatal(err)
	}
	file, err := restarted.acquireLease()
	if err != nil {
		t.Fatal("original inode refused", err)
	}
	file.Close()
	if err := os.Remove(strings.TrimSuffix(path, ".lease") + ".lease-identity"); err != nil {
		t.Fatal(err)
	}
	if file, err := restarted.acquireLease(); err == nil {
		file.Close()
		t.Fatal("missing identity adopted")
	}
}

func TestPythonAndGoUseTheSameDurableLease(t *testing.T) {
	root := t.TempDir()
	installer, err := filepath.Abs("../../../../../deploy/install")
	if err != nil {
		t.Fatal(err)
	}
	script := `import sys
sys.path.insert(0,sys.argv[1])
from pathlib import Path
import node_generations as g,node_install as i
identity={"installation_id":"test-installation","generation":1,"specification_digest":sys.argv[4]}
with g.collection_lease(Path(sys.argv[2]),1,i,identity,initialize=sys.argv[3]=="init"): pass
`
	run := func(mode string) error {
		return exec.Command("python3", "-c", script, installer, root, mode, testLeaseIdentity().SpecificationDigest).Run()
	}
	if err := run("init"); err != nil {
		t.Fatal("Python initialization", err)
	}
	path := filepath.Join(root, "state/node/generations/1.lease")
	writeTestFinalGeneration(t, path)
	caller := &ProcessCaller{LeasePath: path, LeaseIdentity: testLeaseIdentity()}
	file, err := caller.acquireLease()
	if err != nil {
		t.Fatal("Go rejected Python identity", err)
	}
	if err := run("collect"); err == nil {
		file.Close()
		t.Fatal("Python collected live Go lease")
	}
	file.Close()
	if err := run("collect"); err != nil {
		t.Fatal("settled lease not collectible", err)
	}
	for _, kind := range []string{"symlink", "hardlink", "fifo"} {
		record := strings.TrimSuffix(path, ".lease") + ".lease-identity"
		original := record + ".original"
		if err := os.Rename(record, original); err != nil {
			t.Fatal(err)
		}
		switch kind {
		case "symlink":
			err = os.Symlink(original, record)
		case "hardlink":
			err = os.Link(original, record)
		case "fifo":
			err = syscall.Mkfifo(record, 0600)
		}
		if err != nil {
			t.Fatal(err)
		}
		if file, err := caller.acquireLease(); err == nil {
			file.Close()
			t.Fatal("invalid identity accepted", kind)
		}
		if err := run("collect"); err == nil {
			t.Fatal("Python accepted invalid identity", kind)
		}
		if err := os.Remove(record); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(original, record); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHelperRequiresFinalConfigWithoutAnyUnfinishedJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "1.lease")
	writeTestLease(t, path)
	caller := &ProcessCaller{LeasePath: path, LeaseIdentity: testLeaseIdentity()}
	for _, suffix := range []string{".preparing", ".collecting", ".dropped"} {
		marker := strings.TrimSuffix(path, ".lease") + suffix
		if err := os.WriteFile(marker, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		if file, err := caller.acquireLease(); err == nil {
			file.Close()
			t.Fatal("unfinished generation was usable", suffix)
		}
		if err := os.Remove(marker); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(strings.TrimSuffix(path, ".lease") + ".json"); err != nil {
		t.Fatal(err)
	}
	if file, err := caller.acquireLease(); err == nil {
		file.Close()
		t.Fatal("unpublished generation was usable")
	}
	writeTestFinalGeneration(t, path)
	file, err := caller.acquireLease()
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
}

func TestOriginalFinalConfigAndWrongGeneration(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "state/node/generations")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "1.lease")
	writeTestLease(t, path)
	if err := os.Rename(filepath.Join(dir, "1.json"), filepath.Join(root, "provider.json")); err != nil {
		t.Fatal(err)
	}
	caller := &ProcessCaller{LeasePath: path, LeaseIdentity: testLeaseIdentity()}
	file, err := caller.acquireLease()
	if err != nil {
		t.Fatal("initial published config refused", err)
	}
	file.Close()
	raw, _ := json.Marshal(map[string]any{"installation_id": "test-installation", "generation": 2, "provider": "microsandbox", "specification": sandbox.DeploymentSpec{}})
	if err := os.WriteFile(filepath.Join(root, "provider.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if file, err := caller.acquireLease(); err == nil {
		file.Close()
		t.Fatal("another generation final config accepted")
	}
}
