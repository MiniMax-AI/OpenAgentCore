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
	maxActive := flags.Int("max-active", 4, "active reservation capacity (register only)")
	maxRetained := flags.Int("max-retained", 16, "retained allocation capacity (register only)")
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
	if built.Provider == nil {
		return errors.New("node configuration requires one local provider backend")
	}
	if built.Suspension != nil {
		supplied := map[string]bool{}
		flags.Visit(func(f *flag.Flag) { supplied[f.Name] = true })
		if !supplied["max-active"] {
			*maxActive = built.Suspension.MaxActive
		}
		if !supplied["max-retained"] {
			*maxRetained = built.Suspension.MaxRetained
		}
		if *maxActive > built.Suspension.MaxActive || *maxRetained > built.Suspension.MaxRetained {
			return errors.New("node capacity exceeds configured provider limits")
		}
	}
	expected := node.Identity{InstallationID: built.InstallationID, Provider: config.Provider, BackendFingerprint: built.BackendFingerprint, MaxActive: *maxActive, MaxRetained: *maxRetained}
	probe := func(ctx context.Context) (node.Health, error) {
		err := built.Probe(ctx)
		return node.Health{ProviderReady: err == nil}, err
	}
	if args[0] == "register" {
		if *maxActive < 1 || *maxRetained < *maxActive {
			return errors.New("invalid node capacity")
		}
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
			return errors.New("local provider readiness check failed")
		}
		stored, err := node.Enroll(ctx, *coreURL, *stateDir, token, node.EnrollmentRequest{Name: *name})
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(node.EnrollmentResponse{NodeID: stored.Identity.NodeID, InstallationID: stored.Identity.InstallationID, Provider: stored.Identity.Provider})
	}
	stored, err := node.LoadIdentity(*stateDir)
	if err != nil {
		return err
	}
	expected.NodeID = stored.Identity.NodeID
	if expected.InstallationID != stored.Identity.InstallationID || expected.Provider != stored.Identity.Provider || expected.BackendFingerprint != stored.Identity.BackendFingerprint {
		return errors.New("provider configuration differs from retained node identity")
	}
	if *coreURL != "" && *coreURL != stored.CoreURL {
		return errors.New("run uses the retained Core URL")
	}
	return node.Run(ctx, node.AgentConfig{CoreURL: stored.CoreURL, StateDirectory: *stateDir, Identity: stored.Identity, Credential: stored.Credential, Provider: built.Provider, Probe: probe})
}
