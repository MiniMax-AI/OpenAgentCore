package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// releaseMembers are the archive files a Compose installation keeps for Web's
// node payload; the rest of the release archive is only checksummed.
var releaseMembers = []string{"manifest.json", "SHA256SUMS", "node-install.pyz", "runtime/seccomp.json"}

var dataOwners = []struct {
	name string
	uid  int
}{{"database", 70}, {"secrets", 65532}, {"state", 65532}, {"node-payload", 65532}}

var chown = os.Chown

type releaseIdentity struct {
	revision, base, checksum string
}

func releaseFromEnv() (releaseIdentity, error) {
	identity := releaseIdentity{os.Getenv("OAC_REVISION"), os.Getenv("OAC_RELEASE_BASE"), os.Getenv("OAC_ARCHIVE_CHECKSUM")}
	for name, value := range map[string]string{"OAC_REVISION": identity.revision, "OAC_RELEASE_BASE": identity.base, "OAC_ARCHIVE_CHECKSUM": identity.checksum} {
		if value == "" {
			return identity, errors.New(name + " is required")
		}
	}
	return identity, nil
}

func initCommand(ctx context.Context) error {
	release, err := releaseFromEnv()
	if err != nil {
		return err
	}
	syscall.Umask(0o077)
	return initialize("/data", release, func() (map[string][]byte, error) { return downloadRelease(ctx, release) })
}

func downloadRelease(ctx context.Context, release releaseIdentity) (map[string][]byte, error) {
	fmt.Println("Downloading and verifying the matched node installation metadata")
	archive := "oac-" + release.revision + "-linux-amd64"
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, release.base+archive+".tar.gz", nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release metadata download returned HTTP %d", response.StatusCode)
	}
	return readRelease(response.Body, release)
}

func readRelease(body io.Reader, release releaseIdentity) (map[string][]byte, error) {
	archive := "oac-" + release.revision + "-linux-amd64/"
	hash := sha256.New()
	stream := io.TeeReader(body, hash)
	compressed, err := gzip.NewReader(stream)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	wanted := map[string]bool{}
	for _, name := range releaseMembers {
		wanted[archive+name] = true
	}
	entries := tar.NewReader(compressed)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if !wanted[header.Name] {
			continue
		}
		name := header.Name[len(archive):]
		if _, seen := files[name]; seen || header.Typeflag != tar.TypeReg || header.Size > 1<<20 {
			return nil, errors.New("invalid release metadata member")
		}
		if files[name], err = io.ReadAll(entries); err != nil {
			return nil, err
		}
	}
	if _, err := io.Copy(io.Discard, stream); err != nil {
		return nil, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != release.checksum || len(files) != len(releaseMembers) {
		return nil, errors.New("release metadata checksum mismatch")
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
