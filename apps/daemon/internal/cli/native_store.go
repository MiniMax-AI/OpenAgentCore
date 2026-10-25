package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
)

// The Runtime home keeps one installed copy of each component per daemon version, in
// components/<version>/<name>. Every installation of that version on this machine uses it.
// The lock on components/ covers user entries, collection and installing missing components.
var (
	nativeStoreWait  = 10 * time.Minute
	nativeStoreRetry = time.Second
)

func nativeStoreDirectory() (string, error) {
	home, err := paths.Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "components", Version), nil
}

// requireNativeDirectory accepts a real directory owned by the current user, never a link.
func requireNativeDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || !ownedByCurrentUser(info) {
		return fmt.Errorf("%s must be a directory owned by the current user, not a link", dir)
	}
	return nil
}

// lockNativeStore waits for another process holding the store; any other lock failure is final.
func lockNativeStore(ctx context.Context, out io.Writer, root string) (func(), error) {
	if _, err := os.Lstat(root); err == nil {
		if err = requireNativeDirectory(root); err != nil {
			return nil, err
		}
	}
	if err := runtimefs.EnsurePrivateDir(root); err != nil {
		return nil, err
	}
	held, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(nativeStoreWait)
	for waiting := false; ; waiting = true {
		unlock, err := runtimefs.LockDirectory(held)
		if err == nil {
			return func() { unlock(); held.Close() }, nil
		}
		if !errors.Is(err, runtimefs.ErrLocked) {
			held.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			held.Close()
			return nil, errors.New("another installation on this machine has held the shared components for 10 minutes; wait for it to finish or stop it, then rerun the command")
		}
		if !waiting {
			fmt.Fprintln(out, "Waiting for another installation on this machine...")
		}
		select {
		case <-ctx.Done():
			held.Close()
			return nil, ctx.Err()
		case <-time.After(nativeStoreRetry):
		}
	}
}

// resolvedNativeDirectory is the spelling that user entries record and hash.
func resolvedNativeDirectory(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// nativeUserEntry names the file that records an installation as a user of a version directory.
func nativeUserEntry(version, installation string) string {
	sum := sha256.Sum256([]byte(resolvedNativeDirectory(installation)))
	return filepath.Join(version, "users", hex.EncodeToString(sum[:]))
}

// addNativeUser records the installation; an unchanged entry is left alone, a new one is written atomically.
func addNativeUser(version, installation string) error {
	entry := nativeUserEntry(version, installation)
	want := []byte(resolvedNativeDirectory(installation))
	if got, err := os.ReadFile(entry); err == nil && string(got) == string(want) {
		return nil
	}
	dir := filepath.Dir(entry)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	held, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer held.Close()
	return runtimefs.WritePrivateAtomic(held, filepath.Base(entry), want)
}

// countNativeUsers drops the entries of installations whose directory is gone and counts the rest.
// Whatever it cannot read or parse counts as a user. managed is false for a directory without a
// real users directory, which this store did not create.
func countNativeUsers(version string) (users int, managed bool) {
	dir := filepath.Join(version, "users")
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return 0, false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 1, true
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue // an interrupted atomic write
		}
		file := filepath.Join(dir, entry.Name())
		// An entry is dropped only when its installation directory is gone.
		if raw, err := os.ReadFile(file); err == nil && filepath.IsAbs(string(raw)) {
			if _, err = os.Stat(string(raw)); errors.Is(err, os.ErrNotExist) && os.Remove(file) == nil {
				continue
			}
		}
		users++
	}
	return users, true
}

// collectNativeStore removes every version directory that no installation uses. A directory is
// renamed away before it is deleted, so a version is either whole or gone; a rename that fails,
// as on Windows while a file is in use, keeps the version. Leftovers of an interrupted deletion are swept.
func collectNativeStore(root string) error {
	var failure error
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		dir := filepath.Join(root, entry.Name())
		if strings.HasPrefix(entry.Name(), ".remove-") {
			_ = removeNativeTree(dir)
			continue
		}
		if !entry.IsDir() {
			continue // never follow a link out of the store
		}
		if users, managed := countNativeUsers(dir); managed && users == 0 {
			if err := removeNativeVersion(dir); err != nil && failure == nil {
				failure = err
			}
		}
	}
	return failure
}

func removeNativeVersion(dir string) error {
	gone := filepath.Join(filepath.Dir(dir), fmt.Sprintf(".remove-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err := os.Rename(dir, gone); err != nil {
		return fmt.Errorf("cannot remove %s: %w", dir, err)
	}
	return removeNativeTree(gone)
}

// releaseNativeStore drops one installation's entry and collects the store. It returns how many
// other installations still use the version.
func releaseNativeStore(ctx context.Context, out io.Writer, version, entry string) (int, error) {
	root := filepath.Dir(version)
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	unlock, err := lockNativeStore(ctx, out, root)
	if err != nil {
		return 0, err
	}
	defer unlock()
	if err = os.Remove(entry); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	if err = collectNativeStore(root); err != nil {
		return 0, err
	}
	if _, err = os.Lstat(version); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	users, _ := countNativeUsers(version)
	if users == 0 {
		return 0, fmt.Errorf("%s was not removed", version)
	}
	return users, nil
}

// setNativeReadOnly makes the contents of a component unchangeable: files 0400 or, when
// executable, 0500, directories 0500. The top directory stays as it is, because macOS refuses to
// rename a directory that is not writable; sealNativeComponent closes it after the rename.
func setNativeReadOnly(path string) error {
	var directories []string
	err := filepath.WalkDir(path, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name != path {
				directories = append(directories, name)
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0400)
		if info.Mode().Perm()&0100 != 0 {
			mode = 0500
		}
		return os.Chmod(name, mode)
	})
	for i := len(directories) - 1; err == nil && i >= 0; i-- {
		err = os.Chmod(directories[i], 0500)
	}
	return err
}

func sealNativeComponent(dir string) error { return os.Chmod(dir, 0500) }

// removeNativeTree deletes a tree that setNativeReadOnly protected. Go's RemoveAll
// cannot delete read-only entries on Windows and cannot enter read-only directories elsewhere.
func removeNativeTree(path string) error {
	_ = filepath.WalkDir(path, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			return os.Chmod(name, 0700)
		}
		return os.Chmod(name, 0600)
	})
	return os.RemoveAll(path)
}

// nativeDeleteCommand is the command that deletes a read-only component directory by hand.
func nativeDeleteCommand(dir string) string {
	if runtime.GOOS == "windows" {
		return "Remove-Item -Recurse -Force '" + strings.ReplaceAll(dir, "'", "''") + "'"
	}
	quoted := "'" + strings.ReplaceAll(dir, "'", `'\''`) + "'"
	return "chmod -R u+w " + quoted + " && rm -rf " + quoted
}

// cleanNativeStaging removes the unpublished copies an interrupted installation left. The caller holds the store lock.
func cleanNativeStaging(version string) error {
	entries, err := os.ReadDir(version)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".install-") {
			if err = removeNativeTree(filepath.Join(version, entry.Name())); err != nil {
				return fmt.Errorf("install: cannot remove interrupted staging: %w", err)
			}
		}
	}
	return nil
}
