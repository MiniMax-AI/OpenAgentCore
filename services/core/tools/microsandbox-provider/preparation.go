//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

// The preparation cache keeps one archive of the prepared /environment
// directories per Core-supplied key and image digest in a private
// directory under the Runtime home. The guest never sees it. An entry is valid
// for preparationTTL after its save.
const preparationTTL = 24 * time.Hour

// preparedDirectories are what preparation writes, as created by the bootstrap.
// runtime-state belongs to the running daemon and is never copied.
var preparedDirectories = []string{"initialization", "packages", "workspace"}

// The store holds at most this many bytes. A measured Environment archive is
// tens of MiB, so this keeps over a hundred of them and bounds the node's disk
// use; the TTL alone does not, because a busy node can save many keys in a day.
const maxPreparationBytes = 8 << 30

func (b backend) preparationStore() string {
	return filepath.Join(b.q.Config.RuntimeHome, "preparation")
}

// preparationEntry names the archive for this key, scoped by the Runtime image digest.
func (b backend) preparationEntry() string {
	_, digest, _ := strings.Cut(b.q.Config.Image, "@")
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s", digest, b.q.Key)))
	return filepath.Join(b.preparationStore(), hex.EncodeToString(sum[:])+".tar")
}

func validPreparation(path string, now time.Time) bool {
	st, err := os.Lstat(path)
	return err == nil && st.Mode().IsRegular() && now.Sub(st.ModTime()) < preparationTTL
}

// preparationSandbox connects to the running compute and returns the host
// directory behind /environment.
func (b backend) preparationSandbox(ctx context.Context) (*sdk.Sandbox, string, error) {
	h, _, err := b.inspect(ctx, b.q.Compute)
	if err != nil {
		return nil, "", err
	}
	live, err := h.Connect(ctx)
	if err != nil {
		return nil, "", err
	}
	var config sdk.SandboxConfig
	if json.Unmarshal([]byte(h.ConfigJSON()), &config) != nil || config.Labels[workspacePathLabel] == "" {
		live.Detach(context.Background())
		return nil, "", sandbox.ErrInvalid
	}
	return live, config.Labels[workspacePathLabel], nil
}

func guestRun(ctx context.Context, live *sdk.Sandbox, script string) error {
	result, err := runCommand(ctx, live, sandbox.Command{Args: []string{"/bin/sh", "-c", script}, Directory: "/"}, "0:0")
	if err == nil && result.ExitCode != 0 {
		err = errors.New("preparation command failed")
	}
	return err
}

// hostTar copies the SDK's ownership xattrs of an external directory verbatim,
// so the helper never interprets them.
func hostTar(ctx context.Context, stdin *os.File, args ...string) error {
	cmd := exec.CommandContext(ctx, "tar", append([]string{"--xattrs", "--xattrs-include=*"}, args...)...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	return cmd.Run()
}

func (b backend) restorePreparation(ctx context.Context) (bool, error) {
	entry := b.preparationEntry()
	if !validPreparation(entry, time.Now()) {
		return false, nil
	}
	ctx, cancel := context.WithDeadline(ctx, b.q.Deadline)
	defer cancel()
	live, external, err := b.preparationSandbox(ctx)
	if err != nil {
		return false, err
	}
	defer live.Detach(context.Background())
	// The running guest sees the extracted files at once.
	f, err := os.Open(entry)
	if err != nil {
		return false, nil
	}
	defer f.Close()
	if err = hostTar(ctx, f, "-C", external, "-xf", "-"); err != nil {
		// A failed extraction fails this Session; later Sessions prepare in full.
		_ = os.Remove(entry)
	}
	return err == nil, err
}

func (b backend) savePreparation(ctx context.Context) error {
	ctx, cancel := context.WithDeadline(ctx, b.q.Deadline)
	defer cancel()
	live, external, err := b.preparationSandbox(ctx)
	if err != nil {
		return err
	}
	defer live.Detach(context.Background())
	store := b.preparationStore()
	if err = privateDirectory(store); err != nil {
		return err
	}
	temporary := filepath.Join(store, ".tmp-"+uuid.NewString())
	defer os.Remove(temporary)
	if err = guestRun(ctx, live, "sync"); err == nil {
		err = hostTar(ctx, nil, append([]string{"-C", external, "-cf", temporary}, preparedDirectories...)...)
	}
	if err != nil {
		return err
	}
	f, err := os.OpenFile(temporary, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(temporary, 0600)
	}
	if err == nil {
		err = os.Rename(temporary, b.preparationEntry())
	}
	if err != nil {
		return err
	}
	_ = prunePreparations(store, time.Now())
	return syncDirectory(store)
}

// prunePreparations deletes expired entries, then the oldest while the store is
// over its bound.
func prunePreparations(store string, now time.Time) error {
	entries, err := os.ReadDir(store)
	if err != nil {
		return err
	}
	type kept struct {
		path     string
		size     int64
		modified time.Time
	}
	var live []kept
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(store, entry.Name())
		if now.Sub(info.ModTime()) >= preparationTTL {
			_ = os.Remove(path)
			continue
		}
		live = append(live, kept{path, info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(live, func(i, j int) bool { return live[i].modified.Before(live[j].modified) })
	for _, entry := range live {
		if total <= maxPreparationBytes {
			break
		}
		_ = os.Remove(entry.path)
		total -= entry.size
	}
	return nil
}
