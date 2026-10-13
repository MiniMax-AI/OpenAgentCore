package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
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
		if os.Args[1] != "init" {
			fmt.Fprintln(os.Stderr, err.Error())
		}
		os.Exit(1)
	}
}

// buildRevision is set by the release build.
var buildRevision = "development"

func usage() {
	fmt.Fprintf(os.Stderr, "oac (%s)\nUsage: oac install|apply|core-key|rotate-core-key\n", buildRevision)
}

func run(ctx context.Context, command string, args []string) error {
	switch command {
	case "install":
		return installCommand(ctx, args)
	case "init":
		return initCommand()
	case "rotate-volume-key":
		return withLock("/data/secrets/init", func() error { return rotateVolumeKey("/data") })
	}
	root, err := installDir()
	if err != nil {
		return err
	}
	runner := execRunner{dir: root}
	switch command {
	case "apply":
		return withLock(root, func() error { return apply(ctx, runner) })
	case "core-key":
		return coreKeyCommand(ctx, runner, args)
	case "rotate-core-key":
		return withLock(root, func() error {
			if err := runner.Run(ctx, "run", "--rm", "--no-deps", "init", "/usr/local/bin/oac", "rotate-volume-key"); err != nil {
				return err
			}
			return runner.Run(ctx, "restart", "core", "web")
		})
	default:
		usage()
		return errors.New("unknown command")
	}
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

func withLock(root string, fn func() error) error {
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
	if err != nil {
		return fmt.Errorf("another operation is using this installation: %w", err)
	}
	defer unlock()
	return fn()
}

func apply(ctx context.Context, runner Runner) error {
	if err := runner.Run(ctx, "run", "--rm", "--no-deps", "--entrypoint", "/usr/local/bin/oac-core", "core", "check-config"); err != nil {
		return errors.New("configuration check failed; no service was changed")
	}
	return runner.Run(ctx, "up", "-d", "--wait")
}

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
