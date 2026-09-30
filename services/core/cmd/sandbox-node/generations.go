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
)

func runGenerations(ctx context.Context, configFile, stateDir string) error {
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
	pending, err := filepath.Glob(filepath.Join(stateDir, "generations", "*.preparing"))
	if err != nil {
		return err
	}
	paths = append(append([]string{configFile}, paths...), pending...)
	values := map[uint64]node.GenerationProvider{}
	recovery := []sandbox.GenerationReference{}
	collection := []sandbox.GenerationReference{}
	seen := map[uint64]providerconfig.Config{}
	closeValues := func() {
		for _, v := range values {
			if v.Close != nil {
				v.Close()
			}
		}
	}
	for _, path := range paths {
		var config providerconfig.Config
		if strings.HasSuffix(path, ".preparing") {
			journal, readErr := readGenerationJournal(path)
			err = readErr
			if err == nil && journal.Configuration == nil {
				err = sandbox.ErrOwnership
			}
			if err == nil {
				config = *journal.Configuration
			}
		} else {
			config, err = providerconfig.Load(path)
		}
		if err != nil {
			closeValues()
			return err
		}
		if config.InstallationID != base.InstallationID || config.Provider != base.Provider {
			closeValues()
			return sandbox.ErrOwnership
		}
		if path != configFile && strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".json"), ".preparing") != strconv.FormatUint(config.Generation, 10) {
			closeValues()
			return sandbox.ErrOwnership
		}
		state, err := generationLocalState(config, stateDir)
		if err != nil {
			closeValues()
			return err
		}
		if state == "dropped" {
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
		if state == "preparing" {
			recovery = append(recovery, sandbox.GenerationReference{Generation: config.Generation, SpecificationDigest: config.Specification.Digest(config.Provider)})
			continue
		}
		if state == "collecting" {
			collection = append(collection, sandbox.GenerationReference{Generation: config.Generation, SpecificationDigest: config.Specification.Digest(config.Provider)})
			continue
		}
		value, err := buildGeneration(config, stateDir)
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
	manager, err := node.NewGenerationManager(ctx, node.GenerationManagerOptions{Initial: initial, Recover: recovery, Collect: collection,
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
			value, err := buildGeneration(config, stateDir)
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

func buildGeneration(config providerconfig.Config, stateDir string) (node.GenerationProvider, error) {
	if config.Microsandbox != nil {
		directory := filepath.Join(stateDir, "generations")
		if err := os.MkdirAll(directory, 0700); err != nil {
			return node.GenerationProvider{}, err
		}
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
			return node.GenerationProvider{}, sandbox.ErrOwnership
		}
		config.Microsandbox.HelperLeaseGeneration = config.Generation
		config.Microsandbox.HelperLeasePath = filepath.Join(directory, strconv.FormatUint(config.Generation, 10)+".lease")
	}
	built, closeProvider, err := providerconfig.Build(config)
	if err != nil {
		return node.GenerationProvider{}, err
	}
	probe := built.Probe
	if micro := config.Microsandbox; micro != nil {
		probe = func(ctx context.Context) error {
			if err := built.Probe(ctx); err != nil {
				return err
			}
			command := exec.CommandContext(ctx, micro.RuntimePath, "image", "inspect", micro.Image, "--format", "json")
			command.Env = append([]string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + os.Getenv("HOME")}, "MSB_BACKEND=local", "MSB_HOME="+micro.RuntimeHome, "MSB_PATH="+micro.RuntimePath, "MSB_LIBKRUNFW_PATH="+micro.FirmwarePath)
			reader, err := command.StdoutPipe()
			if err != nil {
				return sandbox.ErrRuntimeImageUnavailable
			}
			if err := command.Start(); err != nil {
				return sandbox.ErrRuntimeImageUnavailable
			}
			raw, readErr := io.ReadAll(io.LimitReader(reader, 64*1024+1))
			if len(raw) > 64*1024 {
				_ = command.Process.Kill()
			}
			waitErr := command.Wait()
			var image struct{ Digest, Architecture, OS string }
			if readErr != nil || waitErr != nil || len(raw) > 64*1024 || json.Unmarshal(raw, &image) != nil || image.Digest != strings.SplitN(micro.Image, "@", 2)[1] || image.Architecture != "amd64" || image.OS != "linux" {
				return sandbox.ErrRuntimeImageUnavailable
			}
			return nil
		}
	}
	return node.GenerationProvider{Generation: config.Generation, SpecificationDigest: built.SpecificationDigest, Provider: built.Provider, Probe: probe, Close: closeProvider}, nil
}

type generationJournal struct {
	InstallationID      string                 `json:"installation_id"`
	Generation          uint64                 `json:"generation"`
	SpecificationDigest string                 `json:"specification_digest"`
	NativeComplete      json.RawMessage        `json:"native_complete,omitempty"`
	ImportStarted       json.RawMessage        `json:"import_started,omitempty"`
	Configuration       *providerconfig.Config `json:"configuration,omitempty"`
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

// The preparation configuration is an immutable plan, never a usable provider.
// Only Docker's two specification-proven local IDs can differ at publication.
func sameGenerationPlan(final, plan providerconfig.Config) bool {
	if plan.Docker != nil && final.Docker != nil {
		runtime := plan.Specification.Runtime
		if final.Docker.Image != runtime.ImageID && final.Docker.Image != runtime.ImageManifestDigest {
			return false
		}
		copyDocker := *plan.Docker
		copyDocker.Image = final.Docker.Image
		plan.Docker = &copyDocker
	}
	return reflect.DeepEqual(final, plan)
}

// Pending-only entries stay recovery/collection-only. No journal is readiness
// or authority to collect without a fresh current-connection Core grant.
func generationLocalState(config providerconfig.Config, stateDir string) (string, error) {
	directory := filepath.Join(stateDir, "generations")
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != directory || !info.IsDir() || info.Mode().Perm() != 0700 || !ok || owner.Uid != uint32(os.Getuid()) {
		return "", sandbox.ErrOwnership
	}
	state := ""
	for _, suffix := range []string{".preparing", ".collecting", ".dropped"} {
		path := filepath.Join(directory, strconv.FormatUint(config.Generation, 10)+suffix)
		journal, err := readGenerationJournal(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || journal.InstallationID != config.InstallationID || journal.Generation != config.Generation || journal.SpecificationDigest != config.Specification.Digest(config.Provider) {
			return "", sandbox.ErrOwnership
		}
		if suffix == ".preparing" {
			if len(journal.NativeComplete) != 0 || (string(journal.ImportStarted) != "true" && string(journal.ImportStarted) != "false") || journal.Configuration == nil || !sameGenerationPlan(config, *journal.Configuration) {
				return "", sandbox.ErrOwnership
			}
		} else if len(journal.ImportStarted) != 0 || journal.Configuration != nil || suffix == ".collecting" && string(journal.NativeComplete) != "true" && string(journal.NativeComplete) != "false" || suffix == ".dropped" && len(journal.NativeComplete) != 0 {
			return "", sandbox.ErrOwnership
		}
		state = strings.TrimPrefix(suffix, ".")
	}
	return state, nil
}

// Exit 65 is reserved by the private preparer for artifact transfer/provenance.
// Arbitrary provider output never selects a wire diagnostic.
func generationHelperError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 65 {
		return sandbox.ErrRuntimeDownloadFailed
	}
	return errors.New("local generation operation did not complete")
}
