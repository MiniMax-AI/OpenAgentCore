// Package runtimefs provides ordinary Runtime state files and process locks.
// It does not restrict the launching user or implement a sandbox.
package runtimefs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
)

var ErrUnsafe = errors.New("Runtime state path unavailable or invalid")

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00")
}

// ReadPrivate reads a bounded regular state file.
func ReadPrivate(root *os.Root, name string, limit int64) ([]byte, error) {
	file, err := OpenPrivate(root, name, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, ErrUnsafe
	}
	return raw, nil
}

// ReadPrivatePath is for startup credentials outside an already-open root.
func ReadPrivatePath(path string, limit int64) ([]byte, error) {
	if ValidateLocalPath(path) != nil {
		return nil, ErrUnsafe
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrUnsafe
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, ErrUnsafe
	}
	return raw, nil
}

// WritePrivateAtomic writes a fresh state file before replacing the name.
// An old permissive target is replaced, never opened or repaired in place.
func WritePrivateAtomic(root *os.Root, name string, data []byte) error {
	if !validName(name) || PrivateDirectory(root) != nil {
		return ErrUnsafe
	}
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return ErrUnsafe
	}
	temporary := "." + name + "-" + hex.EncodeToString(suffix[:]) + ".tmp"
	file, err := OpenPrivate(root, temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return ErrUnsafe
	}
	if err = replacePrivate(root, temporary, name); err != nil {
		return err
	}
	return SyncDirectory(root)
}

// EnsurePrivateDir creates state directories with normal account permissions.
func EnsurePrivateDir(path string) error {
	if ValidateLocalPath(path) != nil {
		return ErrUnsafe
	}
	if err := ensurePrivateDir(path); err != nil {
		return err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	return PrivateDirectory(root)
}

// PrivateDirectory checks the storage type, not the account's access policy.
func PrivateDirectory(root *os.Root) error {
	info, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return ErrUnsafe
	}
	return nil
}

// OpenPrivate uses default credential-file permissions when creating a file.
// Existing files retain the launching user's normal filesystem permissions.
func OpenPrivate(root *os.Root, name string, flags int) (*os.File, error) {
	if !validName(name) {
		return nil, ErrUnsafe
	}
	file, err := root.OpenFile(name, flags, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, ErrUnsafe
	}
	return file, nil
}

func ensurePrivateDir(path string) error { return os.MkdirAll(path, 0700) }
