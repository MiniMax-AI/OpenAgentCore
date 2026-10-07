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

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
)

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
	directory, err := b.initializationCWD(input.CWD)
	if err != nil {
		return err
	}
	values, err := ReadToolEnvironment()
	if err != nil {
		return &dispatch.InitializationFailure{}
	}
	packages, err := PackageDirectory()
	if err != nil {
		return &dispatch.InitializationFailure{}
	}
	tail, err := agentcapabilities.InitializationArgs(input.Action, input.Command, input.Packages, filepath.Join(packages, "npm"), filepath.Join(packages, "python"))
	if err != nil {
		return err
	}
	var binary string
	var args []string
	switch input.Action {
	case "setup":
		binary, err = initializationBash()
	case "npm", "python":
		if err = os.MkdirAll(packages, 0700); err != nil {
			return &dispatch.InitializationFailure{}
		}
		if input.Action == "npm" {
			binary, args, err = initializationNPM()
		} else {
			binary, err = initializationPython()
		}
	}
	if err != nil {
		return err
	}
	return runInitializationProcess(ctx, binary, append(args, tail...), directory, initializationEnvironment(values))
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
	if !agentcapabilities.ValidToolEnvironment(values, runtime.GOOS == "windows") {
		return agentcapabilities.ErrInvalid
	}
	path, err := initializedToolEnvironmentPath()
	if err != nil {
		return &dispatch.InitializationFailure{}
	}
	packages, err := PackageDirectory()
	if err != nil {
		return &dispatch.InitializationFailure{}
	}
	if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.MkdirAll(packages, 0700) != nil {
		return &dispatch.InitializationFailure{}
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return &dispatch.InitializationFailure{}
	}
	defer root.Close()
	unlock, err := runtimefs.LockDirectory(root)
	if err != nil {
		return &dispatch.InitializationFailure{}
	}
	defer unlock()
	if _, err = root.Stat(filepath.Base(path)); !errors.Is(err, os.ErrNotExist) {
		return &dispatch.InitializationFailure{}
	}
	configured := make(map[string]string, len(values)+2)
	if source := os.Getenv("OAC_RUNTIME_TOOL_ENV_FILE"); source != "" {
		if runtimefs.ValidateLocalPath(source) != nil {
			return &dispatch.InitializationFailure{}
		}
		local, err := readToolEnvironmentFile(source)
		if err != nil {
			return &dispatch.InitializationFailure{}
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
	npmBin := filepath.Join(packages, "npm", "bin")
	pythonBin := filepath.Join(packages, "python", "bin")
	if runtime.GOOS == "windows" {
		npmBin = filepath.Join(packages, "npm")
		pythonBin = filepath.Join(packages, "python", "Scripts")
	}
	configured = agentcapabilities.ToolEnvironment(configured, os.Getenv("PATH"), npmBin, pythonBin, filepath.Join(packages, "python"), string(os.PathListSeparator))
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
		return &dispatch.InitializationFailure{}
	}
	defer root.Close()
	if root.MkdirAll(filepath.Dir(relative), 0700) != nil {
		return &dispatch.InitializationFailure{}
	}
	parent, err := root.OpenRoot(filepath.Dir(relative))
	if err != nil {
		return &dispatch.InitializationFailure{}
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
	return fmt.Errorf("Runtime initialization requires %s: %w", name, &dispatch.InitializationFailure{})
}
