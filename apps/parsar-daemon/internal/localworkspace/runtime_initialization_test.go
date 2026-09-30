package localworkspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// The test executable doubles as a native package-manager fixture on all OSes.
// Dispatch before testing parses npm/pip arguments; no dependencies are fetched.
func init() {
	if os.Getenv("OAC_INITIALIZATION_PACKAGE_FIXTURE") != "1" {
		return
	}
	raw, _ := json.Marshal(os.Args[1:])
	if os.WriteFile(os.Getenv("OAC_INITIALIZATION_PACKAGE_RECEIPT"), raw, 0600) != nil {
		os.Exit(9)
	}
	os.Exit(0)
}

func TestRuntimeInitializationPackageTargets(t *testing.T) {
	b := initializationFixture(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	for _, name := range []string{"node", "npm", "python3"} {
		if name == "npm" && runtime.GOOS == "windows" {
			if err = os.WriteFile(filepath.Join(bin, "npm.cmd"), []byte("@exit /b 99\r\n"), 0600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err = os.WriteFile(filepath.Join(bin, name+suffix), source, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS == "windows" {
		cli := filepath.Join(bin, "node_modules", "npm", "bin", "npm-cli.js")
		if err = os.MkdirAll(filepath.Dir(cli), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(cli, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	receipt := filepath.Join(t.TempDir(), "args.json")
	if err = b.initializeRuntime(t.Context(), proto.RuntimeInitialization{Action: "configure", Env: map[string]string{"OAC_INITIALIZATION_PACKAGE_FIXTURE": "1", "OAC_INITIALIZATION_PACKAGE_RECEIPT": receipt}}); err != nil {
		t.Fatal(err)
	}
	packages, _ := PackageDirectory()
	for _, action := range []string{"npm", "python"} {
		if err = b.initializeRuntime(t.Context(), proto.RuntimeInitialization{Action: action, Network: "enabled", Packages: []string{"name with spaces", "second"}}); err != nil {
			t.Fatal(action, err)
		}
		raw, err := os.ReadFile(receipt)
		if err != nil {
			t.Fatal(err)
		}
		var args []string
		if json.Unmarshal(raw, &args) != nil {
			t.Fatal("invalid fixture receipt")
		}
		if action == "npm" && runtime.GOOS == "windows" {
			cli := filepath.Join(bin, "node_modules", "npm", "bin", "npm-cli.js")
			if len(args) == 0 || args[0] != cli {
				t.Fatal("initializer bypassed the shared npm shim resolver", args)
			}
		}
		flag, target := "--prefix", filepath.Join(packages, "npm")
		if action == "python" {
			flag, target = "--target", filepath.Join(packages, "python")
		}
		found := false
		for i := 0; i+1 < len(args); i++ {
			if args[i] == flag && args[i+1] == target {
				found = true
			}
		}
		if !found || len(args) < 3 || args[len(args)-3] != "--" || args[len(args)-2] != "name with spaces" || args[len(args)-1] != "second" {
			t.Fatal("package argv or local target mismatch", args)
		}
	}
}

func TestRuntimeInitializationChild(t *testing.T) {
	mode := os.Getenv("OAC_INITIALIZATION_FIXTURE")
	if mode == "" {
		return
	}
	directory := os.Getenv("OAC_INITIALIZATION_FIXTURE_DIR")
	switch mode {
	case "complete":
		if os.Getenv("RUNTIME_TEST_PRIVATE") != "" {
			os.Exit(9)
		}
		os.Stdout.Write(bytes.Repeat([]byte("private-output"), 10000))
		os.Stderr.Write(bytes.Repeat([]byte("private-error"), 10000))
	case "fail":
		os.Exit(7)
	case "child":
		time.Sleep(time.Second)
		_ = os.WriteFile(filepath.Join(directory, "late"), []byte("bad"), 0600)
	case "parent":
		binary, _ := os.Executable()
		child := exec.Command(binary, "-test.run=^TestRuntimeInitializationChild$")
		child.Env = append(initializationEnvironment(nil), "OAC_INITIALIZATION_FIXTURE=child", "OAC_INITIALIZATION_FIXTURE_DIR="+directory)
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if child.Start() != nil {
			os.Exit(8)
		}
		_ = os.WriteFile(filepath.Join(directory, "started"), []byte("ready"), 0600)
		_ = child.Wait()
	}
	os.Exit(0)
}

func initializationFixture(t *testing.T) *Binding {
	t.Helper()
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	t.Setenv("OAC_RUNTIME_INITIALIZATION_DIRECTORY", "")
	t.Setenv("OAC_RUNTIME_PACKAGE_DIRECTORY", "")
	t.Setenv("OAC_RUNTIME_TOOL_ENV_FILE", "")
	return &Binding{workspace: t.TempDir(), writer: &fileWriter{}}
}

func TestRuntimeInitializationConfigurationAndDirectories(t *testing.T) {
	b := initializationFixture(t)
	config, err := InitializationDirectory()
	if err != nil || config != filepath.Join(os.Getenv("OAC_RUNTIME_HOME"), "initialization") {
		t.Fatal(config, err)
	}
	packages, err := PackageDirectory()
	if err != nil || packages != filepath.Join(os.Getenv("OAC_RUNTIME_HOME"), "packages") {
		t.Fatal(packages, err)
	}
	env := map[string]string{"VALUE": "'\n$(not-a-command)", "PYTHONPATH": "operator-path"}
	if err = b.initializeRuntime(t.Context(), proto.RuntimeInitialization{Action: "configure", Env: env}); err != nil {
		t.Fatal(err)
	}
	values, err := ReadToolEnvironment()
	if err != nil || values["VALUE"] != env["VALUE"] || !strings.HasPrefix(values["PYTHONPATH"], filepath.Join(packages, "python")) {
		t.Fatal("configuration mismatch", err)
	}
	if b.initializeRuntime(t.Context(), proto.RuntimeInitialization{Action: "configure", Env: map[string]string{"VALUE": "changed"}}) == nil {
		t.Fatal("configuration replaced")
	}
	values, _ = ReadToolEnvironment()
	if values["VALUE"] != env["VALUE"] {
		t.Fatal("configuration changed")
	}
	for setting, resolver := range map[string]func() (string, error){"OAC_RUNTIME_INITIALIZATION_DIRECTORY": InitializationDirectory, "OAC_RUNTIME_PACKAGE_DIRECTORY": PackageDirectory} {
		selected := t.TempDir()
		t.Setenv(setting, selected)
		if actual, err := resolver(); err != nil || actual != selected {
			t.Fatal("operator directory ignored", err)
		}
	}
}

func TestRuntimeInitializationExplicitToolEnvironment(t *testing.T) {
	b := initializationFixture(t)
	file := filepath.Join(t.TempDir(), "explicit.json")
	original := []byte(`{"DECLARED":"value","OVERRIDE":"local"}`)
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_RUNTIME_TOOL_ENV_FILE", file)
	env, err := ReadOptionalToolEnvironment()
	if err != nil || env["DECLARED"] != "value" {
		t.Fatal("explicit tool environment", err)
	}
	if err := b.initializeRuntime(t.Context(), proto.RuntimeInitialization{Action: "configure", Env: map[string]string{"OVERRIDE": "session"}}); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(file); err != nil || string(raw) != string(original) {
		t.Fatal("operator source was modified", err)
	}
	// Reconnect reads the frozen result even after the operator edits its source.
	if err := os.WriteFile(file, []byte(`{"DECLARED":"changed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	env, err = ReadOptionalToolEnvironment()
	if err != nil || env["DECLARED"] != "value" || env["OVERRIDE"] != "session" {
		t.Fatal("tool snapshot was not frozen", err)
	}
	if b.initializeRuntime(t.Context(), proto.RuntimeInitialization{Action: "configure"}) == nil {
		t.Fatal("configuration replay succeeded")
	}
}

func TestRuntimeInitializationRejectsMissingExplicitToolEnvironment(t *testing.T) {
	b := initializationFixture(t)
	t.Setenv("OAC_RUNTIME_TOOL_ENV_FILE", filepath.Join(t.TempDir(), "missing.json"))
	if b.initializeRuntime(t.Context(), proto.RuntimeInitialization{Action: "configure"}) == nil {
		t.Fatal("missing explicit environment was ignored")
	}
	path, err := initializedToolEnvironmentPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed configuration left a prepared snapshot")
	}
}

func TestRuntimeInitializationSetupUsesBashAndPhysicalWorkspace(t *testing.T) {
	b := initializationFixture(t)
	if _, err := initializationBash(); err != nil {
		t.Skip("Bash unavailable; native dependency failure is tested separately")
	}
	if err := b.initializeRuntime(t.Context(), proto.RuntimeInitialization{Action: "configure", Env: map[string]string{"VALUE": "configured"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(b.workspace, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.workspace, "proof.sh"), []byte("printf skill-proof"), 0600); err != nil {
		t.Fatal(err)
	}
	err := b.initializeRuntime(t.Context(), proto.RuntimeInitialization{Action: "setup", Network: "enabled", CWD: "/workspace/sub", Command: `. ../proof.sh > proof; printf '%s' "$VALUE" > value`})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"proof": "skill-proof", "value": "configured"} {
		raw, err := os.ReadFile(filepath.Join(b.workspace, "sub", name))
		if err != nil || string(raw) != want {
			t.Fatal(name, err)
		}
	}
}

func TestRuntimeInitializationMissingDependenciesAndInvalidRequests(t *testing.T) {
	b := initializationFixture(t)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CLAUDE_CODE_GIT_BASH_PATH", "")
	if err := b.initializeRuntime(t.Context(), proto.RuntimeInitialization{Action: "configure"}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []proto.RuntimeInitialization{{Action: "setup", Network: "enabled", Command: "true"}, {Action: "npm", Network: "enabled", Packages: []string{"valid"}}, {Action: "python", Network: "enabled", Packages: []string{"valid"}}} {
		var failed *InitializationFailure
		if err := b.initializeRuntime(t.Context(), input); !errors.As(err, &failed) || !strings.Contains(err.Error(), "requires") {
			t.Fatal("missing dependency was not explicit", input.Action, err)
		}
	}
	for _, input := range []proto.RuntimeInitialization{{Action: "system"}, {Action: "setup", Network: "enabled", Command: "true", CWD: "/workspace/../outside"}, {Action: "npm", Network: "enabled", Packages: []string{"--unsafe"}}} {
		if !errors.Is(b.initializeRuntime(t.Context(), input), agentcapabilities.ErrInvalid) {
			t.Fatal("invalid request accepted", input.Action)
		}
	}
}

func TestRuntimeInitializationProcessSettlesAndDiscardsOutput(t *testing.T) {
	t.Setenv("RUNTIME_TEST_PRIVATE", "do-not-inherit")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	env := append(initializationEnvironment(nil), "OAC_INITIALIZATION_FIXTURE=complete")
	if err = runInitializationProcess(t.Context(), binary, []string{"-test.run=^TestRuntimeInitializationChild$"}, directory, env); err != nil {
		t.Fatal(err)
	}
	env = append(initializationEnvironment(nil), "OAC_INITIALIZATION_FIXTURE=fail")
	err = runInitializationProcess(t.Context(), binary, []string{"-test.run=^TestRuntimeInitializationChild$"}, directory, env)
	var failed *InitializationFailure
	if !errors.As(err, &failed) || failed.ExitCode == nil || *failed.ExitCode != 7 {
		t.Fatal("exit status", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	env = append(initializationEnvironment(nil), "OAC_INITIALIZATION_FIXTURE=parent", "OAC_INITIALIZATION_FIXTURE_DIR="+directory)
	done := make(chan error, 1)
	go func() {
		done <- runInitializationProcess(ctx, binary, []string{"-test.run=^TestRuntimeInitializationChild$"}, directory, env)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(directory, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("fixture startup timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err = <-done; !errors.Is(err, ErrInitializationUnconfirmed) {
		t.Fatal("cancel should be unknown", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err = os.Stat(filepath.Join(directory, "late")); !os.IsNotExist(err) {
		t.Fatal("descendant survived initialization")
	}
}

func TestRuntimeInitialFileAtomicReplacement(t *testing.T) {
	b := initializationFixture(t)
	path := "/workspace/nested/child/file"
	for _, body := range [][]byte{nil, []byte("first"), bytes.Repeat([]byte("x"), 1<<20), []byte("replacement")} {
		if err := b.installInitialFile(t.Context(), proto.RuntimeInitialFile{Path: path}, body); err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(filepath.Join(b.workspace, "nested", "child", "file"))
		if err != nil || !bytes.Equal(actual, body) {
			t.Fatal("atomic replacement", err)
		}
	}
	for _, invalid := range []string{"/elsewhere/file", "/workspace/../outside", "/workspace/a\\b"} {
		if !errors.Is(b.installInitialFile(t.Context(), proto.RuntimeInitialFile{Path: invalid}, []byte("x")), agentcapabilities.ErrInvalid) {
			t.Fatal("invalid path accepted")
		}
	}
}
func TestRuntimePreparationRejectsFilesAfterFinalization(t *testing.T) {
	b, req := testBinding(t)
	configured, err := b.Configure(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Prepare(t.Context(), configured); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(nil)
	input := proto.RuntimePreparePayload{Step: "begin", EnvironmentID: b.environment, SessionID: b.capabilityIdentity().SessionID,
		Action: "file", File: &proto.RuntimeInitialFile{Path: "/workspace/file"}, SHA256: hex.EncodeToString(digest[:])}
	if err = b.ApplyRuntimePreparation(t.Context(), input, nil); !errors.Is(err, agentcapabilities.ErrInvalid) {
		t.Fatal("finalized Runtime accepted file", err)
	}
	input.Action = "initialize"
	input.File = nil
	input.SHA256 = ""
	input.Initialization = &proto.RuntimeInitialization{Action: "configure", Env: map[string]string{}}
	if err = b.ApplyRuntimePreparation(t.Context(), input, nil); !errors.Is(err, agentcapabilities.ErrInvalid) {
		t.Fatal("finalized Runtime accepted initialize", err)
	}
}
