// sandbox-node hosts the selected local Provider and dials its owning Core.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	providerconfig "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/config"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/node"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	if len(args) == 0 || (args[0] != "register" && args[0] != "run") {
		return errors.New("usage: parsar-sandbox-node register|run --config PATH --state-dir PATH")
	}
	flags := flag.NewFlagSet("sandbox-node "+args[0], flag.ContinueOnError)
	configFile := flags.String("config", "", "absolute provider configuration file")
	stateDir := flags.String("state-dir", "", "absolute private node state directory")
	coreURL := flags.String("core-url", "", "Core HTTPS origin (register only)")
	name := flags.String("name", "sandbox-node", "display name (register only)")
	tokenFile := flags.String("enrollment-token-file", "", "private single-use enrollment token file (register only)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if !filepath.IsAbs(*configFile) || !filepath.IsAbs(*stateDir) {
		return errors.New("config and state-dir must be absolute paths")
	}
	config, err := providerconfig.Load(*configFile)
	if err != nil {
		return err
	}
	built, closeProvider, err := providerconfig.Build(config)
	if err != nil {
		return err
	}
	defer closeProvider()
	log.Ctx(ctx).Info("sandbox node configuration loaded", "config_path", *configFile)
	if built.Provider == nil {
		return errors.New("node configuration requires one local provider backend")
	}
	expected := node.Identity{SpecificationDigest: built.SpecificationDigest, DeploymentGeneration: config.Generation, InstallationID: built.InstallationID, Provider: config.Provider, BackendFingerprint: built.BackendFingerprint}
	probe := func(ctx context.Context) (node.Health, error) {
		err := built.Probe(ctx)
		return node.Health{ProviderReady: err == nil}, err
	}
	if args[0] == "register" {
		if !filepath.IsAbs(*tokenFile) {
			return errors.New("enrollment-token-file must be absolute")
		}

		fd, err := syscall.Open(*tokenFile, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err != nil {
			return errors.New("cannot open enrollment token")
		}
		file := os.NewFile(uintptr(fd), "enrollment token")
		st, err := file.Stat()
		if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
			file.Close()
			return errors.New("enrollment token must be a private regular file (0600)")
		}
		raw, err := io.ReadAll(io.LimitReader(file, 4097))
		file.Close()
		if err != nil {
			return errors.New("cannot read enrollment token")
		}
		token := strings.TrimSpace(string(raw))
		if token == "" || len(token) > 4096 {
			return errors.New("invalid enrollment token")
		}
		if _, err = node.InitIdentity(*stateDir, *coreURL, expected); err != nil {
			return err
		}
		probeCtx, stopProbe := context.WithTimeout(ctx, 5*time.Second)
		_, err = probe(probeCtx)
		stopProbe()
		if err != nil {
			return fmt.Errorf("local provider readiness check failed (%s): %w", sandbox.NodeDiagnostic(err), err)
		}
		stored, err := node.Enroll(ctx, *coreURL, *stateDir, token, node.EnrollmentRequest{Name: *name})
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(node.EnrollmentResponse{MaxActive: stored.Identity.MaxActive, MaxRetained: stored.Identity.MaxRetained, SpecificationDigest: stored.Identity.SpecificationDigest, DeploymentGeneration: stored.Identity.DeploymentGeneration, NodeID: stored.Identity.NodeID, InstallationID: stored.Identity.InstallationID, Provider: stored.Identity.Provider})
	}
	stored, err := node.RefreshIdentity(ctx, *stateDir)
	if err != nil {
		return err
	}
	expected.NodeID = stored.Identity.NodeID
	if expected.SpecificationDigest != stored.Identity.SpecificationDigest || expected.DeploymentGeneration != stored.Identity.DeploymentGeneration || expected.InstallationID != stored.Identity.InstallationID || expected.Provider != stored.Identity.Provider || expected.BackendFingerprint != stored.Identity.BackendFingerprint {
		return errors.New("provider configuration differs from retained node identity")
	}
	if *coreURL != "" && *coreURL != stored.CoreURL {
		return errors.New("run uses the retained Core URL")
	}
	return node.Run(ctx, node.AgentConfig{CoreURL: stored.CoreURL, StateDirectory: *stateDir, Identity: stored.Identity, Credential: stored.Credential, Provider: built.Provider, Probe: probe})
}
