package localworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/runtimefs"
)

// InitializationFailure contains only a confirmed step's safe exit status.
type InitializationFailure struct{ ExitCode *int }

func (*InitializationFailure) Error() string { return "Runtime initialization failed" }

var ErrInitializationUnconfirmed = errors.New("Runtime initialization unconfirmed")

func (b *Binding) initializeRuntime(ctx context.Context, input proto.RuntimeInitialization) error {
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > proto.RuntimePrepareMaxFrameBytes {
		return agentcapabilities.ErrInvalid
	}
	if ctx.Err() != nil {
		return ErrInitializationUnconfirmed
	}
	if input.Action == "configure" {
		return configureRuntime(input.Env)
	}
	if input.Action != "setup" && input.Action != "npm" && input.Action != "python" {
		return agentcapabilities.ErrInvalid
	}
	if input.Network != "enabled" && input.Network != "disabled" {
		return agentcapabilities.ErrInvalid
	}
	directory, err := b.initializationCWD(input.CWD)
	if err != nil {
		return err
	}
	values, err := ReadToolEnvironment()
	if err != nil {
		return &InitializationFailure{}
	}
	packages, err := PackageDirectory()
	if err != nil {
		return &InitializationFailure{}
	}
	var binary string
	var args []string
	switch input.Action {
	case "setup":
		if input.Command == "" || strings.ContainsRune(input.Command, 0) {
			return agentcapabilities.ErrInvalid
		}
		binary, err = initializationBash()
		args = []string{"--noprofile", "--norc", "-c", input.Command}
	case "npm", "python":
		if len(input.Packages) == 0 {
			return agentcapabilities.ErrInvalid
		}
		for _, value := range input.Packages {
			if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "\x00\r\n") {
				return agentcapabilities.ErrInvalid
			}
		}
		if err = os.MkdirAll(packages, 0700); err != nil {
			return &InitializationFailure{}
		}
		if input.Action == "npm" {
			binary, args, err = initializationNPM()
			args = append(args, "install", "--global", "--prefix", filepath.Join(packages, "npm"), "--")
		} else {
			binary, err = initializationPython()
			args = []string{"-m", "pip", "install", "--disable-pip-version-check", "--no-input", "--target", filepath.Join(packages, "python"), "--"}
		}
		args = append(args, input.Packages...)
	}
	if err != nil {
		return err
	}
	return runInitializationProcess(ctx, binary, args, directory, initializationEnvironment(values))
}

func (b *Binding) initializationCWD(value string) (string, error) {
	if value == "" || value == "/workspace" {
		return b.workspace, nil
	}
	if !strings.HasPrefix(value, "/workspace/") {
		return "", agentcapabilities.ErrInvalid
	}
	relative, err := nativeAPIPath(strings.TrimPrefix(value, "/workspace/"))
	if err != nil {
		return "", agentcapabilities.ErrInvalid
	}
	return filepath.Join(b.workspace, relative), nil
}

func configureRuntime(values map[string]string) error {
	if !validToolEnvironment(values) {
		return agentcapabilities.ErrInvalid
	}
	path, err := initializedToolEnvironmentPath()
	if err != nil {
		return &InitializationFailure{}
	}
	packages, err := PackageDirectory()
	if err != nil {
		return &InitializationFailure{}
	}
	if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.MkdirAll(packages, 0700) != nil {
		return &InitializationFailure{}
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return &InitializationFailure{}
	}
	defer root.Close()
	unlock, err := runtimefs.LockDirectory(root)
	if err != nil {
		return &InitializationFailure{}
	}
	defer unlock()
	if _, err = root.Stat(filepath.Base(path)); !errors.Is(err, os.ErrNotExist) {
		return &InitializationFailure{}
	}
	configured := make(map[string]string, len(values)+2)
	if source := os.Getenv("OAC_RUNTIME_TOOL_ENV_FILE"); source != "" {
		if runtimefs.ValidateLocalPath(source) != nil {
			return &InitializationFailure{}
		}
		local, err := readToolEnvironmentFile(source)
		if err != nil {
			return &InitializationFailure{}
		}
		for key, value := range local {
			if runtime.GOOS == "windows" {
				key = strings.ToUpper(key)
			}
			configured[key] = value
		}
	}
	// Explicit Session values override the operator's base tool configuration.
	for key, value := range values {
		if runtime.GOOS == "windows" {
			key = strings.ToUpper(key)
		}
		configured[key] = value
	}
	separator := string(os.PathListSeparator)
	npmBin := filepath.Join(packages, "npm", "bin")
	pythonBin := filepath.Join(packages, "python", "bin")
	if runtime.GOOS == "windows" {
		npmBin = filepath.Join(packages, "npm")
		pythonBin = filepath.Join(packages, "python", "Scripts")
	}
	pathValue, exists := configured["PATH"]
	if !exists {
		pathValue = os.Getenv("PATH")
	}
	configured["PATH"] = npmBin + separator + pythonBin + separator + pathValue
	pythonPath := configured["PYTHONPATH"]
	configured["PYTHONPATH"] = filepath.Join(packages, "python")
	if pythonPath != "" {
		configured["PYTHONPATH"] += separator + pythonPath
	}
	raw, err := json.Marshal(configured)
	if err != nil || len(raw) > proto.RuntimePrepareMaxFrameBytes {
		return agentcapabilities.ErrInvalid
	}
	if runtimefs.WritePrivateAtomic(root, filepath.Base(path), raw) != nil {
		return ErrInitializationUnconfirmed
	}
	return nil
}

func (b *Binding) installInitialFile(ctx context.Context, input proto.RuntimeInitialFile, data []byte) (err error) {
	if b.writer == nil || !strings.HasPrefix(input.Path, "/workspace/") || len(data) > proto.RuntimePrepareMaxBytes {
		return agentcapabilities.ErrInvalid
	}
	relative, err := nativeAPIPath(strings.TrimPrefix(input.Path, "/workspace/"))
	if err != nil {
		return agentcapabilities.ErrInvalid
	}
	if ctx.Err() != nil {
		return ErrInitializationUnconfirmed
	}
	w := b.writer
	if !w.mu.TryLock() {
		return agentcapabilities.ErrInvalid
	}
	defer w.mu.Unlock()
	if w.uncertain {
		return ErrInitializationUnconfirmed
	}
	defer func() { w.uncertain = errors.Is(err, ErrInitializationUnconfirmed) }()
	root, err := os.OpenRoot(b.workspace)
	if err != nil {
		return &InitializationFailure{}
	}
	defer root.Close()
	if root.MkdirAll(filepath.Dir(relative), 0700) != nil {
		return &InitializationFailure{}
	}
	parent, err := root.OpenRoot(filepath.Dir(relative))
	if err != nil {
		return &InitializationFailure{}
	}
	defer parent.Close()
	// Initial files replace existing contents, unlike public Files create. Finish
	// the synchronous atomic write before returning even if cancellation arrives.
	if runtimefs.WritePrivateAtomic(parent, filepath.Base(relative), data) != nil {
		return ErrInitializationUnconfirmed
	}
	return nil
}

func initializationDependency(name string) error {
	return fmt.Errorf("Runtime initialization requires %s: %w", name, &InitializationFailure{})
}
