package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

// buildRevision is set by the release build.
var buildRevision = "development"

func usage() {
	fmt.Fprintf(os.Stderr, "oac (%s)\nUsage: oac apply|core-key|rotate-core-key|init\n", buildRevision)
}

func run(ctx context.Context, command string, args []string) error {
	switch command {
	case "init":
		return initCommand(ctx)
	}
	root, err := installDir()
	if err != nil {
		return err
	}
	in := installation{root: root, data: dataDir(root)}
	runner := execRunner{dir: root}
	switch command {
	case "apply":
		return withLock(root, func() error { return apply(ctx, runner) })
	case "core-key":
		return coreKeyCommand(ctx, in, runner, args)
	case "rotate-core-key":
		return withLock(root, func() error { return rotateCoreKey(ctx, in, runner) })
	default:
		usage()
		return errors.New("unknown command")
	}
}

func installDir() (string, error) {
	if dir := os.Getenv("OAC_INSTALL_DIR"); dir != "" {
		return dir, nil
	}
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

type installation struct{ root, data string }

func dataDir(root string) string {
	if dir := os.Getenv("OAC_DATA_MOUNT"); dir != "" {
		return dir
	}
	return filepath.Join(root, "data")
}

func withLock(root string, fn func() error) error {
	file, err := os.OpenFile(filepath.Join(root, ".oac.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return fn()
}

func apply(ctx context.Context, runner Runner) error {
	if err := runner.Run(ctx, "run", "--rm", "--no-deps", "--entrypoint", "/usr/local/bin/oac-core", "core", "check-config"); err != nil {
		return errors.New("configuration check failed; no service was changed")
	}
	return runner.Run(ctx, "up", "-d", "--wait")
}

func coreKeyCommand(ctx context.Context, in installation, runner Runner, args []string) error {
	path := filepath.Join(in.data, "secrets", "web", "core.key")
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

func rotateCoreKey(ctx context.Context, in installation, runner Runner) error {
	key, err := generateCoreKey()
	if err != nil {
		return err
	}
	keyPath := filepath.Join(in.data, "secrets", "web", "core.key")
	digestPath := filepath.Join(in.data, "secrets", "core", "core-key-digests.json")
	if err := writeSecret(keyPath, key+"\n"); err != nil {
		return err
	}
	raw, err := json.Marshal([]string{keyDigest(key)})
	if err != nil {
		return err
	}
	if err := writeSecret(digestPath, string(raw)+"\n"); err != nil {
		return err
	}
	return runner.Run(ctx, "restart", "core", "web")
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

func writeSecret(path, contents string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return os.ErrInvalid
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(contents), 0o600); err != nil {
		return err
	}
	if err := os.Chown(temporary, int(stat.Uid), int(stat.Gid)); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return os.Rename(temporary, path)
}
