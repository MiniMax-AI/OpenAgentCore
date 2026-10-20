package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	providerconfig "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	workspaceproviders "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs/providers"
)

func runGenerations(ctx context.Context, registry *providerconfig.Registry, configFile, stateDir string) error {
	root := filepath.Dir(configFile)
	if stateDir != filepath.Join(root, "state", "node") {
		return errors.New("generation state must belong to the installed node root")
	}
	stored, err := node.RefreshIdentity(ctx, stateDir)
	if err != nil {
		return err
	}
	base, err := providerconfig.Load(configFile)
	if err != nil {
		return err
	}
	if base.InstallationID != stored.Identity.InstallationID || base.Provider != stored.Identity.Provider || base.Generation != stored.Identity.DeploymentGeneration || base.Specification.Digest(base.Provider) != stored.Identity.SpecificationDigest {
		return sandbox.ErrOwnership
	}
	paths, err := filepath.Glob(filepath.Join(stateDir, "generations", "*.json"))
	if err != nil {
		return err
	}
	paths = append([]string{configFile}, paths...)
	values := map[uint64]node.GenerationProvider{}
	recovery := []sandbox.GenerationReference{}
	seen := map[uint64]sandbox.NodeConfig{}
	closeValues := func() {
		for _, v := range values {
			if v.Close != nil {
				v.Close()
			}
		}
	}
	for _, path := range paths {
		config, err := providerconfig.Load(path)
		if err != nil {
			closeValues()
			return err
		}
		if config.InstallationID != base.InstallationID || config.Provider != base.Provider {
			closeValues()
			return sandbox.ErrOwnership
		}
		if path != configFile && strings.TrimSuffix(filepath.Base(path), ".json") != strconv.FormatUint(config.Generation, 10) {
			closeValues()
			return sandbox.ErrOwnership
		}
		dropped, err := generationDropped(config, stateDir)
		if err != nil {
			closeValues()
			return err
		}
		if dropped {
			continue
		}
		if previous, ok := seen[config.Generation]; ok {
			if !sameGenerationPlan(previous, config) {
				closeValues()
				return sandbox.ErrOwnership
			}
			continue
		}
		seen[config.Generation] = config
		value, err := buildGeneration(registry, config, stateDir)
		if errors.Is(err, os.ErrNotExist) {
			recovery = append(recovery, sandbox.GenerationReference{Generation: config.Generation, SpecificationDigest: config.Specification.Digest(config.Provider)})
			continue
		}
		if err != nil {
			closeValues()
			return err
		}
		values[config.Generation] = value
	}
	helper := filepath.Join(root, "generation-preparer.pyz")
	runHelper := func(ctx context.Context, action string, generation uint64, digest string) error {
		command := exec.CommandContext(ctx, "python3", helper, "--installation-id", base.InstallationID, "--generation-action", action, "--generation", strconv.FormatUint(generation, 10), "--specification-digest", digest)
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		// Only the preparation/collection process group is canceled. Native sandbox
		// helpers retain their independent allocation-lock completion semantics.
		command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
		command.Stdout = os.Stderr
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			return generationHelperError(ctx, err)
		}
		return nil
	}
	initial := make([]node.GenerationProvider, 0, len(values))
	for _, v := range values {
		initial = append(initial, v)
	}
	manager, err := node.NewGenerationManager(ctx, node.GenerationManagerOptions{Initial: initial, Recover: recovery,
		Prepare: func(ctx context.Context, generation uint64, digest string) (node.GenerationProvider, error) {
			if err := runHelper(ctx, "prepare", generation, digest); err != nil {
				return node.GenerationProvider{}, err
			}
			config, err := providerconfig.Load(filepath.Join(stateDir, "generations", strconv.FormatUint(generation, 10)+".json"))
			if err != nil {
				return node.GenerationProvider{}, err
			}
			if config.InstallationID != base.InstallationID || config.Provider != base.Provider || config.Generation != generation || config.Specification.Digest(config.Provider) != digest {
				return node.GenerationProvider{}, sandbox.ErrOwnership
			}
			value, err := buildGeneration(registry, config, stateDir)
			if err != nil {
				return value, err
			}
			return value, nil
		},
		Remove: func(ctx context.Context, value node.GenerationProvider) error {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			return runHelper(ctx, "collect", value.Generation, value.SpecificationDigest)
		},
	})
	if err != nil {
		closeValues()
		return err
	}
	defer manager.Close()
	return node.Run(ctx, node.AgentConfig{CoreURL: stored.CoreURL, StateDirectory: stateDir, Identity: stored.Identity, Credential: stored.Credential, Generations: manager})
}

func buildGeneration(registry *providerconfig.Registry, config sandbox.NodeConfig, stateDir string) (node.GenerationProvider, error) {
	built, closeProvider, err := registry.Build(config, sandbox.LocalOptions{GenerationStateDirectory: stateDir, Workspace: workspaceproviders.New()})
	if err != nil {
		return node.GenerationProvider{}, err
	}
	return node.GenerationProvider{Generation: config.Generation, SpecificationDigest: built.SpecificationDigest, Provider: built.Provider, Probe: built.Probe, Quiescent: built.Quiescent, Close: closeProvider}, nil
}

type generationJournal struct {
	InstallationID      string `json:"installation_id"`
	Generation          uint64 `json:"generation"`
	SpecificationDigest string `json:"specification_digest"`
}

func readGenerationJournal(path string) (generationJournal, error) {
	var journal generationJournal
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return journal, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	var st syscall.Stat_t
	err = syscall.Fstat(fd, &st)
	resolved, pathErr := filepath.EvalSymlinks(path)
	if err != nil || pathErr != nil || resolved != path || st.Mode&syscall.S_IFMT != syscall.S_IFREG || st.Mode&0777 != 0600 || st.Uid != uint32(os.Getuid()) || st.Nlink != 1 || st.Size > 16384 {
		return journal, sandbox.ErrOwnership
	}
	decoder := json.NewDecoder(io.LimitReader(file, 16385))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&journal)
	var trailing any
	end := decoder.Decode(&trailing)
	opened, statErr := file.Stat()
	named, namedErr := os.Lstat(path)
	if err != nil || end != io.EOF || statErr != nil || namedErr != nil || !os.SameFile(opened, named) {
		return journal, sandbox.ErrOwnership
	}
	return journal, nil
}

// sameGenerationPlan compares the Core-visible envelope of two copies of one
// generation; native is the adapter's node-local state.
func sameGenerationPlan(final, plan sandbox.NodeConfig) bool {
	final.Native, plan.Native = nil, nil
	return reflect.DeepEqual(final, plan)
}

// generationDropped reports whether the permanent dropped receipt names this
// generation. No receipt is readiness or authority to collect without a fresh
// current-connection Core grant.
func generationDropped(config sandbox.NodeConfig, stateDir string) (bool, error) {
	directory := filepath.Join(stateDir, "generations")
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != directory || !info.IsDir() || info.Mode().Perm() != 0700 || !ok || owner.Uid != uint32(os.Getuid()) {
		return false, sandbox.ErrOwnership
	}
	journal, err := readGenerationJournal(filepath.Join(directory, strconv.FormatUint(config.Generation, 10)+".dropped"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || journal.InstallationID != config.InstallationID || journal.Generation != config.Generation || journal.SpecificationDigest != config.Specification.Digest(config.Provider) {
		return false, sandbox.ErrOwnership
	}
	return true, nil
}

func generationHelperError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New("local generation operation did not complete")
}
