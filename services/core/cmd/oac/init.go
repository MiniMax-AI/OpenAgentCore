package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"
)

// releaseMembers are the only files copied from the initialization image.
var releaseMembers = []string{"manifest.json", "SHA256SUMS", "node-install.pyz", "runtime/seccomp.json"}

var dataOwners = []struct {
	name string
	uid  int
}{{"database", 70}, {"secrets", 65532}, {"state", 65532}, {"node-payload", 65532}}

var chown = os.Chown

type releaseIdentity struct {
	revision string
}

func initCommand() error {
	revision := os.Getenv("OAC_REVISION")
	if revision == "" {
		return errors.New("OAC_REVISION is required")
	}
	if revision != buildRevision {
		return errors.New("initialization image does not match the Compose release")
	}
	syscall.Umask(0o077)
	release := releaseIdentity{revision}
	return initialize("/data", release, func() (map[string][]byte, error) {
		return readRelease("/opt/oac/node-payload", release)
	})
}

func readRelease(root string, release releaseIdentity) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, name := range releaseMembers {
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return nil, errors.New("invalid release metadata member")
		}
		files[name], err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}
	}
	sums := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(files["SHA256SUMS"])), "\n") {
		checksum, name, ok := strings.Cut(line, "  ")
		if !ok || sums[name] != "" {
			return nil, errors.New("invalid release metadata checksums")
		}
		sums[name] = checksum
	}
	for name, data := range files {
		if name == "SHA256SUMS" {
			continue
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != sums[name] {
			return nil, errors.New("release metadata checksum mismatch")
		}
	}
	var manifest struct {
		SourceCommit string `json:"source_commit"`
		Platform     string `json:"platform"`
	}
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		return nil, err
	}
	if manifest.SourceCommit != release.revision || manifest.Platform != "linux/amd64" {
		return nil, errors.New("release identity mismatch")
	}
	fmt.Println("Bundled node installation metadata verified")
	return files, nil
}

func writeOwned(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return err
	}
	if err := chown(temporary, 65532, 65532); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func ownedDir(path string, mode os.FileMode, uid int) error {
	if err := os.Mkdir(path, mode); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	if err := os.Chmod(path, mode); err != nil {
		return err
	}
	return chown(path, uid, uid)
}

func fileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

type installReceipt struct {
	SourceCommit string            `json:"source_commit"`
	Files        map[string]string `json:"files"`
}

func initialize(root string, release releaseIdentity, fetch func() (map[string][]byte, error)) error {
	if err := os.Chmod(root, 0o755); err != nil {
		return err
	}
	for _, owner := range dataOwners {
		if err := ownedDir(filepath.Join(root, owner.name), 0o700, owner.uid); err != nil {
			return err
		}
	}
	for _, name := range []string{"core", "web", "database"} {
		if err := ownedDir(filepath.Join(root, "secrets", name), 0o700, 65532); err != nil {
			return err
		}
	}
	lock, err := os.OpenFile(filepath.Join(root, "secrets", ".init.lock"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	marker := filepath.Join(root, "installation.json")
	if raw, err := os.ReadFile(marker); err == nil {
		var receipt installReceipt
		if err := json.Unmarshal(raw, &receipt); err != nil {
			return err
		}
		if receipt.SourceCommit != release.revision {
			return errors.New("this data directory belongs to another release; create a new installation")
		}
		for name, checksum := range receipt.Files {
			actual, err := fileDigest(filepath.Join(root, name))
			if err != nil {
				return err
			}
			if actual != checksum {
				return errors.New("installation files changed; restore the matching data directory")
			}
		}
		fmt.Println("Existing installation verified")
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, name := range []string{"database", "state"} {
		entries, err := os.ReadDir(filepath.Join(root, name))
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return errors.New("existing data requires its original installation files")
		}
	}
	files, err := fetch()
	if err != nil {
		return err
	}
	prefix := "node-payload/releases/" + release.revision + "/"
	names := []string{}
	for _, name := range releaseMembers {
		if err := writeOwned(filepath.Join(root, prefix+name), files[name]); err != nil {
			return err
		}
		names = append(names, prefix+name)
	}
	active, _ := json.Marshal(map[string]string{"source_commit": release.revision})
	if err := writeOwned(filepath.Join(root, "node-payload", "active.json"), active); err != nil {
		return err
	}
	if err := filepath.WalkDir(filepath.Join(root, "node-payload"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return err
		}
		if err := os.Chmod(path, 0o755); err != nil {
			return err
		}
		return chown(path, 65532, 65532)
	}); err != nil {
		return err
	}
	generators := []struct {
		name     string
		generate func() string
	}{
		{"secrets/web/core.key", func() string {
			key, err := generateCoreKey()
			if err != nil {
				panic(err)
			}
			return key
		}},
		{"secrets/database/password", func() string { return randomHex(32) }},
		{"secrets/core/credential.key", func() string { return base64.StdEncoding.EncodeToString(randomBytes(32)) }},
		{"secrets/core/installation.id", func() string { return uuid.NewString() }},
	}
	for _, secret := range generators {
		path := filepath.Join(root, secret.name)
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			if err := writeOwned(path, []byte(secret.generate()+"\n")); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		names = append(names, secret.name)
	}
	key, err := coreKey(root)
	if err != nil {
		return err
	}
	digests, _ := json.Marshal([]string{keyDigest(key)})
	if err := writeOwned(filepath.Join(root, "secrets", "core", "core-key-digests.json"), digests); err != nil {
		return err
	}
	names = append(names, "secrets/core/core-key-digests.json", "node-payload/active.json")
	receipt := installReceipt{SourceCommit: release.revision, Files: map[string]string{}}
	for _, name := range names {
		if receipt.Files[name], err = fileDigest(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	raw, _ := json.Marshal(receipt)
	if err := writeOwned(marker, raw); err != nil {
		return err
	}
	fmt.Println("Installation initialized; print the sign-in key with: docker compose exec web oac-web core-key")
	return nil
}

func randomBytes(n int) []byte {
	value := make([]byte, n)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return value
}

func randomHex(n int) string { return hex.EncodeToString(randomBytes(n)) }
