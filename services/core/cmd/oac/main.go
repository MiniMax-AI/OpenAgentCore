package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if os.Args[1] == "init" {
		log.Init(log.Config{})
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1], os.Args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, failureMessage(ctx, os.Args[1], err))
		os.Exit(1)
	}
}

// failureMessage reports an interrupt as one. Whatever the command had started stays valid, so repeating it continues.
func failureMessage(ctx context.Context, command string, err error) string {
	if ctx.Err() == nil {
		return err.Error()
	}
	if command == "install" {
		return "Interrupted; rerun the installer to continue."
	}
	return "Interrupted; rerun oac " + command + " to continue."
}

// buildRevision is set by the release build.
var buildRevision = "development"

func usageText() string {
	return fmt.Sprintf("oac (%s)\nUsage: oac install|apply|core-key|rotate-core-key|uninstall", buildRevision)
}

func usage() { fmt.Fprintln(os.Stderr, usageText()) }

func run(ctx context.Context, command string, args []string) error {
	switch command {
	case "install":
		return installCommand(ctx, args)
	case "init":
		return initCommand()
	case "uninstall":
		return uninstallCommand(ctx, args)
	case "rotate-volume-key":
		return withLock("/data/secrets/init", func() error { return rotateVolumeKey("/data") })
	case "apply", "core-key", "rotate-core-key":
	default:
		return fmt.Errorf("Unknown command %q.\n%s", command, usageText())
	}
	root, err := installDir()
	if err != nil {
		return err
	}
	runner := execRunner{dir: root}
	switch command {
	case "apply":
		return withLock(root, func() error {
			if err := apply(ctx, runner, root, "fix the cause and run oac apply again"); err != nil {
				return err
			}
			fmt.Println("Services are running with the current settings.")
			return nil
		})
	case "core-key":
		return coreKeyCommand(ctx, runner, args)
	case "rotate-core-key":
		return withLock(root, func() error {
			if err := runner.Run(ctx, "run", "--rm", "--no-deps", "init", "/usr/local/bin/oac", "rotate-volume-key"); err != nil {
				return err
			}
			if err := runner.Run(ctx, "restart", "core", "web"); err != nil {
				return err
			}
			fmt.Printf("Core key rotated. The old key no longer works and console sessions ended. Read the new key with: %s core-key --show\n", quote(filepath.Join(root, cliName())))
			return nil
		})
	}
	return nil
}

func installDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); err == nil {
		return dir, nil
	}
	return "", errors.New("run oac from an installation directory that contains compose.yaml")
}

func withLock(root string, fn func() error) (err error) {
	defer func() { err = accessError(root, err) }()
	dir := root + ".lock"
	if info, err := os.Lstat(dir); err == nil && !info.IsDir() {
		return errors.New("invalid installation lock directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	handle, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	unlock, err := runtimefs.LockDirectory(handle)
	if errors.Is(err, fs.ErrPermission) {
		return err
	} else if err != nil {
		return errors.New("Another oac command is running on this installation; wait for it to finish and retry.")
	}
	defer unlock()
	return fn()
}

// accessError explains a permission failure on the installation's own paths or
// the directories above them inside the home directory. An earlier run with sudo
// typically leaves them owned by root.
func accessError(root string, err error) error {
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || !errors.Is(err, fs.ErrPermission) {
		return err
	}
	// A directory this account cannot enter hides every path below it.
	path := pathErr.Path
	for path != filepath.Dir(path) {
		if _, err := os.Lstat(path); err == nil {
			break
		}
		path = filepath.Dir(path)
	}
	owned := []string{root, root + ".lock", root + ".staging"}
	var targets []string
	home, _ := os.UserHomeDir()
	separator := string(filepath.Separator)
	switch {
	case slices.Contains(owned, path):
		for _, name := range owned {
			if _, err := os.Lstat(name); err == nil {
				targets = append(targets, quote(name))
			}
		}
	case home != "" && strings.HasPrefix(root, path+separator) && strings.HasPrefix(path, home+separator):
		targets = []string{quote(path)}
	default:
		return err
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("This account cannot access %s; give it access and retry.", path)
	}
	return fmt.Errorf("This account cannot access %s. If an earlier run used sudo, return ownership to this account and retry: sudo chown -R \"$(id -un)\" %s", path, strings.Join(targets, " "))
}

var (
	initVolume  = []string{"run", "--rm", "--no-deps", "init"}
	checkConfig = []string{"run", "--rm", "--no-deps", "--entrypoint", "/usr/local/bin/oac-core", "core", "check-config"}
	// Starting recreates changed services, so a resumed upgrade leaves no container on the previous release.
	startServices = []string{"up", "-d", "--wait", "--wait-timeout", "180", "--pull", "never", "--remove-orphans"}
)

// apply is the start shared by oac apply and every installation. Init runs
// first: it refuses a data volume committed to another release and finishes one
// whose first start was interrupted, before check-config reads its secrets.
// Only a failed start can be fixed by repeating the command, so only it carries
// the advice in rerun.
func apply(ctx context.Context, runner Runner, dir, rerun string) error {
	if err := runner.Run(ctx, initVolume...); err != nil {
		return fmt.Errorf("data volume check failed; no service was changed: %w", err)
	}
	if err := runner.Run(ctx, checkConfig...); err != nil {
		return fmt.Errorf("configuration check failed; no service was changed: %w", err)
	}
	err := runner.Run(ctx, startServices...)
	if err == nil || ctx.Err() != nil {
		return err
	}
	message := fmt.Sprintf("startup failed; data retained, %s: %v", rerun, err)
	if names, states := unhealthyServices(ctx, runner); len(names) > 0 {
		message += fmt.Sprintf("\nNot healthy: %s\nSee why: docker compose -f %s logs --tail 50 %s", strings.Join(states, ", "), quote(filepath.Join(dir, "compose.yaml")), strings.Join(names, " "))
	}
	return errors.New(message)
}

// unhealthyServices lists the services that are not running and healthy, by name and as "name (state)". A finished init is healthy.
func unhealthyServices(ctx context.Context, runner Runner) (names, states []string) {
	raw, err := runner.Output(ctx, "ps", "--all", "--format", "json")
	if err != nil {
		return nil, nil
	}
	for decoder := json.NewDecoder(bytes.NewReader(raw)); ; {
		var s struct {
			Service, State, Health string
			ExitCode               int
		}
		if decoder.Decode(&s) != nil {
			return names, states
		}
		state := s.State
		if s.Health != "" {
			state = s.Health
		}
		if state == "healthy" || state == "running" || (s.State == "exited" && s.ExitCode == 0) {
			continue
		}
		names = append(names, s.Service)
		states = append(states, s.Service+" ("+state+")")
	}
}

// quote makes a path one shell word for a command the user copies.
func quote(name string) string { return "'" + strings.ReplaceAll(name, "'", `'\''`) + "'" }

func coreKeyCommand(ctx context.Context, runner Runner, args []string) error {
	path := "Docker volume: secrets/web/core.key (use oac core-key --show)"
	if len(args) == 0 {
		fmt.Println(path)
		return nil
	}
	if len(args) == 1 && args[0] == "--show" {
		return runner.Run(ctx, "exec", "-T", "web", "/usr/local/bin/oac-web", "core-key")
	}
	return errors.New("Usage: oac core-key [--show]")
}

func generateCoreKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "oac_admin_" + hex.EncodeToString(buf), nil
}

func rotateVolumeKey(data string) error {
	key, err := generateCoreKey()
	if err != nil {
		return err
	}
	keyPath := filepath.Join(data, "secrets", "web", "core.key")
	if err := writeOwned(keyPath, []byte(key+"\n")); err != nil {
		return err
	}
	return syncCoreKeyDigest(data)
}

func syncCoreKeyDigest(data string) error {
	key, err := coreKey(data)
	if err != nil {
		return err
	}
	value, err := hex.DecodeString(strings.TrimPrefix(key, "oac_admin_"))
	if err != nil || !strings.HasPrefix(key, "oac_admin_") || len(value) != 32 {
		return errors.New("invalid saved Core key")
	}
	raw, _ := json.Marshal([]string{keyDigest(key)})
	path := filepath.Join(data, "secrets", "core", "core-key-digests.json")
	if saved, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(saved)) == string(raw) {
		return nil
	}
	return writeOwned(path, raw)
}

func coreKey(data string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(data, "secrets", "web", "core.key"))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func keyDigest(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
