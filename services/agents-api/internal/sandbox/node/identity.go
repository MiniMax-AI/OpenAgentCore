package node

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"syscall"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/providers"
	"github.com/google/uuid"
)

type StoredIdentity struct {
	Identity   Identity `json:"identity"`
	Credential string   `json:"credential"`
	CoreURL    string   `json:"core_url"`
	OwnerEpoch uint64   `json:"owner_epoch"`
}

func lockDirectory(dir string) (func(), error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, errors.New("node state directory must be canonical and absolute")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0700 {
		return nil, errors.New("node state directory must be private")
	}
	fd, err := syscall.Open(filepath.Join(dir, "identity.lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = syscall.Close(fd)
		return nil, errors.New("sandbox node identity is already in use")
	}
	return func() { _ = syscall.Flock(fd, syscall.LOCK_UN); _ = syscall.Close(fd) }, nil
}
func readIdentity(dir string) (StoredIdentity, error) {
	var out StoredIdentity
	fd, err := syscall.Open(filepath.Join(dir, "identity.json"), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return out, err
	}
	f := os.NewFile(uintptr(fd), "identity.json")
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return out, errors.New("node identity must be a private regular file")
	}
	d := json.NewDecoder(io.LimitReader(f, 16384))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF || !validID(out.Identity.NodeID) || len(out.Credential) != 64 {
		return StoredIdentity{}, errors.New("invalid node identity")
	}
	secret, err := hex.DecodeString(out.Credential)
	if err != nil || len(secret) != 32 || hex.EncodeToString(secret) != out.Credential {
		return StoredIdentity{}, errors.New("invalid node identity")
	}
	return out, nil
}
func writeIdentity(dir string, s StoredIdentity) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".identity-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, "identity.json")); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// InitIdentity creates the credential before any enrollment request. A lost
// enrollment response can therefore be recovered by authenticating this identity.
func InitIdentity(dir, coreURL string, identity Identity) (StoredIdentity, error) {
	release, err := lockDirectory(dir)
	if err != nil {
		return StoredIdentity{}, err
	}
	defer release()
	return initIdentity(dir, coreURL, identity)
}
func initIdentity(dir, coreURL string, identity Identity) (StoredIdentity, error) {
	if _, err := endpoint(coreURL, ""); err != nil {
		return StoredIdentity{}, err
	}
	stored, err := readIdentity(dir)
	if err == nil {
		expected := identity
		if expected.NodeID == "" {
			expected.NodeID = stored.Identity.NodeID
		}
		if !sameBackend(stored.Identity, expected) || stored.CoreURL != coreURL {
			return StoredIdentity{}, errors.New("node configuration does not match its retained identity")
		}
		return stored, nil
	}
	if !os.IsNotExist(err) {
		return StoredIdentity{}, err
	}
	if identity.NodeID == "" {
		identity.NodeID = uuid.NewString()
	}
	if !validID(identity.NodeID) || !validID(identity.InstallationID) || !providers.IsNode(identity.Provider) || len(identity.BackendFingerprint) != 64 {
		return StoredIdentity{}, errors.New("invalid node configuration")
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return StoredIdentity{}, err
	}
	stored = StoredIdentity{Identity: identity, Credential: hex.EncodeToString(secret), CoreURL: coreURL}
	if err = writeIdentity(dir, stored); err != nil {
		return StoredIdentity{}, err
	}
	return stored, nil
}
func LoadIdentity(dir string) (StoredIdentity, error) {
	release, err := lockDirectory(dir)
	if err != nil {
		return StoredIdentity{}, err
	}
	defer release()
	return readIdentity(dir)
}

func endpoint(raw, path string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("node Core URL must be an origin")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || !(u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()) {
			return "", errors.New("remote node Core URL requires HTTPS")
		}
	}
	u.Path = path
	return u.String(), nil
}
