package main

import (
	"context"
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
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
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

// releaseManifest holds the node manifest fields initialization reads.
// source_time, the commit time in Unix seconds, orders releases.
type releaseManifest struct {
	SourceCommit string `json:"source_commit"`
	SourceTime   int64  `json:"source_time"`
	Platform     string `json:"platform"`
}

func initCommand() error {
	revision := os.Getenv("OAC_REVISION")
	if revision == "" {
		err := errors.New("OAC_REVISION is required")
		log.Bg().Error("Initialization failed", "step", "validate_revision", "error", err)
		return err
	}
	if revision != buildRevision {
		err := errors.New("initialization image does not match the Compose release")
		log.Bg().Error("Initialization failed", "step", "validate_revision", "revision", revision, "error", err)
		return err
	}
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
	var manifest releaseManifest
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

func initialize(root string, release releaseIdentity, fetch func() (map[string][]byte, error)) (err error) {
	ctx, _ := log.StartBackgroundTrace(context.Background())
	logger := log.With("component", "oac-init", "revision", release.revision)
	started := time.Now()
	step, stepStarted := "prepare_directories", started
	logger.InfoContext(ctx, "Initialization started", "data_dir", root)
	logger.InfoContext(ctx, "Initialization step started", "step", step)
	nextStep := func(next string) {
		logger.InfoContext(ctx, "Initialization step completed", "step", step, "duration_ms", time.Since(stepStarted).Milliseconds())
		step, stepStarted = next, time.Now()
		logger.InfoContext(ctx, "Initialization step started", "step", step)
	}
	defer func() {
		if err != nil {
			logger.ErrorContext(ctx, "Initialization failed", "step", step, "duration_ms", time.Since(started).Milliseconds(), "error", err)
			return
		}
		logger.InfoContext(ctx, "Initialization step completed", "step", step, "duration_ms", time.Since(stepStarted).Milliseconds())
		logger.InfoContext(ctx, "Initialization completed", "duration_ms", time.Since(started).Milliseconds())
	}()
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
	nextStep("acquire_lock")
	return withLock(filepath.Join(root, "secrets", "init"), func() error {

		nextStep("verify_existing_installation")
		marker := filepath.Join(root, "installation.json")
		var previous installReceipt
		raw, err := os.ReadFile(marker)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			nextStep("verify_empty_data")
			for _, name := range []string{"database", "state"} {
				entries, err := os.ReadDir(filepath.Join(root, name))
				if err != nil {
					return err
				}
				if len(entries) > 0 {
					return errors.New("existing data requires its original installation files")
				}
			}
		case err != nil:
			return err
		default:
			if err := json.Unmarshal(raw, &previous); err != nil {
				return err
			}
			for name, checksum := range previous.Files {
				actual, err := fileDigest(filepath.Join(root, name))
				if err != nil {
					return err
				}
				if actual != checksum {
					return errors.New("installation files changed; restore the matching data directory")
				}
			}
			if previous.SourceCommit == release.revision {
				// The pointer follows the receipt, so a crash between the two heals here.
				if err := writePointer(root, release); err != nil {
					return err
				}
				if err := syncCoreKeyDigest(root); err != nil {
					return err
				}
				logger.InfoContext(ctx, "Existing installation verified", "file_count", len(previous.Files))
				return nil
			}
		}
		nextStep("verify_bundled_metadata")
		files, err := fetch()
		if err != nil {
			return err
		}
		logger.InfoContext(ctx, "Bundled node installation metadata verified", "file_count", len(files))
		if previous.SourceCommit != "" {
			nextStep("verify_release_order")
			if err := requireNewer(root, previous.SourceCommit, release, files["manifest.json"]); err != nil {
				return err
			}
			logger.InfoContext(ctx, "Upgrading installation", "from_revision", previous.SourceCommit)
		}
		nextStep("publish_node_metadata")
		// The receipt keeps every retained release authenticated; old releases stay
		// for the nodes that still run them.
		names := []string{}
		for name := range previous.Files {
			if strings.HasPrefix(name, "node-payload/releases/") {
				names = append(names, name)
			}
		}
		prefix := "node-payload/releases/" + release.revision + "/"
		for _, name := range releaseMembers {
			if err := writeOwned(filepath.Join(root, prefix+name), files[name]); err != nil {
				return err
			}
			logger.InfoContext(ctx, "Node metadata file copied", "file", name)
			names = append(names, prefix+name)
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
		nextStep("prepare_credentials")
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
				logger.InfoContext(ctx, "Credential file generated", "file", secret.name)
			} else if err != nil {
				return err
			} else {
				logger.InfoContext(ctx, "Credential file retained", "file", secret.name)
			}
			if secret.name != "secrets/web/core.key" {
				names = append(names, secret.name)
			}
		}
		if err := syncCoreKeyDigest(root); err != nil {
			return err
		}

		nextStep("write_installation_receipt")
		receipt := installReceipt{SourceCommit: release.revision, Files: map[string]string{}}
		for _, name := range names {
			checksum, err := fileDigest(filepath.Join(root, name))
			if err != nil {
				return err
			}
			receipt.Files[name] = checksum
		}
		raw, _ = json.Marshal(receipt)
		// The receipt is the commit point; the pointer follows it.
		if err := writeOwned(marker, raw); err != nil {
			return err
		}
		if err := writePointer(root, release); err != nil {
			return err
		}
		logger.InfoContext(ctx, "Installation initialized", "sign_in_key_command", "docker compose exec web oac-web core-key")
		return nil
	})
}

// writePointer selects the installation's release for Web and node installs.
func writePointer(root string, release releaseIdentity) error {
	active, _ := json.Marshal(map[string]string{"source_commit": release.revision})
	return writeOwned(filepath.Join(root, "node-payload", "active.json"), active)
}

// requireNewer refuses a release that is not newer than the installed one by
// commit time. A manifest without source_time reads as 0, the oldest.
func requireNewer(root, installed string, release releaseIdentity, manifest []byte) error {
	raw, err := os.ReadFile(filepath.Join(root, "node-payload", "releases", installed, "manifest.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("installation receipt names release %.12s, whose node metadata is missing; restore the matching data directory", installed)
	}
	if err != nil {
		return err
	}
	var current, next releaseManifest
	if err := json.Unmarshal(raw, &current); err != nil {
		return err
	}
	if err := json.Unmarshal(manifest, &next); err != nil {
		return err
	}
	if next.SourceTime > current.SourceTime {
		return nil
	}
	date := func(seconds int64) string { return time.Unix(seconds, 0).UTC().Format("2006-01-02 15:04 UTC") }
	return fmt.Errorf("This installation runs release %.12s (%s); this release, %.12s (%s), is not newer. Downgrades are not supported: run that release or a newer one, or restore the backup taken before the upgrade.",
		installed, date(current.SourceTime), release.revision, date(next.SourceTime))
}

func randomBytes(n int) []byte {
	value := make([]byte, n)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return value
}

func randomHex(n int) string { return hex.EncodeToString(randomBytes(n)) }
