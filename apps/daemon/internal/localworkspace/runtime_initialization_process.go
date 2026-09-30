package localworkspace

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
)

func initializationBash() (string, error) {
	if runtime.GOOS != "windows" {
		if binary, err := exec.LookPath("bash"); err == nil {
			return binary, nil
		}
		return "", initializationDependency("bash")
	}
	// Git Bash preserves setup's Bash syntax. Never substitute cmd.exe, PowerShell
	// or the Windows WSL launcher for a missing Bash implementation.
	candidates := []string{}
	if explicit := os.Getenv("CLAUDE_CODE_GIT_BASH_PATH"); explicit != "" {
		candidates = append(candidates, explicit)
	} else if git, err := exec.LookPath("git.exe"); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(git), "..", "bin", "bash.exe"), filepath.Join(filepath.Dir(git), "bash.exe"))
	}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if runtimefs.ValidateLocalPath(candidate) != nil {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", initializationDependency("Git Bash")
}

func initializationPython() (string, error) {
	for _, name := range []string{"python3", "python"} {
		if binary, err := exec.LookPath(name); err == nil {
			return binary, nil
		}
	}
	return "", initializationDependency("Python with pip")
}

func initializationNPM() (string, []string, error) {
	if _, err := exec.LookPath("node"); err != nil {
		return "", nil, initializationDependency("Node.js")
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		return "", nil, initializationDependency("npm")
	}
	binary, args, err := ResolvePackageManagerCommand(npm, nil)
	if err != nil {
		return "", nil, initializationDependency("npm CLI")
	}
	return binary, args, nil
}

func initializationEnvironment(configured map[string]string) []string {
	values := map[string]string{}
	for _, key := range []string{"PATH", "HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "PATHEXT", "LANG"} {
		if value := os.Getenv(key); value != "" {
			values[key] = value
		}
	}
	if runtime.GOOS != "windows" && values["LANG"] == "" {
		values["LANG"] = "C.UTF-8"
	}
	for key, value := range configured {
		if runtime.GOOS == "windows" {
			for existing := range values {
				if strings.EqualFold(existing, key) {
					delete(values, existing)
				}
			}
		}
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

// The shared process owner settles the leader and descendants. Readers finish
// before return; output is discarded with constant memory, never put in errors.
func runInitializationProcess(ctx context.Context, binary string, args []string, directory string, env []string) error {
	operation, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if operation.Err() != nil {
		return ErrInitializationUnconfirmed
	}
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return &InitializationFailure{}
	}
	process, err := clirunner.Start(clirunner.StartOptions{Parent: operation, Binary: binary, Args: args,
		Dir: directory, Env: env, OwnProcessGroup: true, KillTimeout: 250 * time.Millisecond})
	if err != nil {
		return ErrInitializationUnconfirmed
	}
	defer process.Cancel()
	finished := make(chan error, 2)
	for _, stream := range []io.Reader{process.Stdout, process.Stderr} {
		go func(stream io.Reader) {
			_, err := io.Copy(io.Discard, stream)
			if err != nil {
				process.Cancel()
			}
			finished <- err
		}(stream)
	}
	first, second := <-finished, <-finished
	_ = process.Wait()
	if operation.Err() != nil || first != nil || second != nil || process.Cmd.ProcessState == nil {
		return ErrInitializationUnconfirmed
	}
	code := process.Cmd.ProcessState.ExitCode()
	if code == 0 {
		return nil
	}
	if code < 1 || code > 255 {
		return ErrInitializationUnconfirmed
	}
	return &InitializationFailure{ExitCode: &code}
}
