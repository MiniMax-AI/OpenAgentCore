package microsandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
)

// ProcessCaller never kills a mutating helper on a Core response timeout.
// The helper retains its allocation flock until the SDK mutation settles,
// including after Core exits. Output is still drained and bounded by this waiter.
type ProcessCaller struct {
	active atomic.Int64
	// LeasePath belongs to the permanent node state namespace, not a release.
	// It is never unlinked, including after the generation is collected.
	LeasePath     string
	LeaseIdentity LeaseIdentity
}

func (p *ProcessCaller) Quiescent() bool { return p.active.Load() == 0 }

func (p *ProcessCaller) Call(ctx context.Context, q Request) (Response, error) {
	data, e := json.Marshal(q)
	if e != nil || len(data) > MaxRequestBytes {
		return Response{}, errors.New("invalid helper request")
	}
	lease, err := p.acquireLease()
	if err != nil {
		return Response{}, errors.New("generation helper lease unavailable")
	}
	releaseLease := func() {
		if lease != nil {
			_ = lease.Close()
		}
	}
	cmd := exec.Command(q.Config.HelperPath)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Env = HelperEnvironment(os.Environ(), q.Config)
	if lease != nil {
		cmd.ExtraFiles = []*os.File{lease}
		cmd.Env = append(cmd.Env, "OAC_NODE_GENERATION_LEASE_FD=3")
	}
	stdout := &limitBuffer{limit: MaxResponseBytes}
	stderr := &limitBuffer{limit: MaxOutputBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	p.active.Add(1)
	if e := cmd.Start(); e != nil {
		p.active.Add(-1)
		releaseLease()
		return Response{}, errors.New("helper unavailable")
	}
	done := make(chan error, 1)
	go func() { err := cmd.Wait(); releaseLease(); p.active.Add(-1); done <- err }()
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case err := <-done:
		if err != nil || stdout.exceeded || stderr.exceeded {
			return Response{}, errors.New("helper result unconfirmed")
		}
	}
	var out Response
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&out); e != nil {
		return Response{}, errors.New("invalid helper response")
	}
	var extra any
	if e := decoder.Decode(&extra); e != io.EOF {
		return Response{}, errors.New("trailing helper response")
	}
	return out, nil
}

// Strip native selector and credential-bearing inherited configuration. Operator
// proxy settings are not passed into guest environments by this adapter.
func HelperEnvironment(env []string, c Config) []string {
	out := []string{}
	for _, v := range env {
		key, _, _ := strings.Cut(v, "=")
		if key == "HOME" || key == "PATH" || key == "TMPDIR" || key == "LANG" || key == "SSL_CERT_FILE" || key == "SSL_CERT_DIR" {
			out = append(out, v)
		}
	}
	return append(out, "MSB_HOME="+c.RuntimeHome, "MSB_PATH="+c.RuntimePath, "MSB_LIBKRUNFW_PATH="+c.FirmwarePath, "MSB_BACKEND=local", "RUST_LOG=off", "NO_COLOR=1")
}

type limitBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitBuffer) Write(v []byte) (int, error) {
	n := len(v)
	left := b.limit - b.Len()
	if n > left {
		b.exceeded = true
		v = v[:left]
	}
	_, _ = b.Buffer.Write(v)
	return n, nil // Drain excess output without unbounded retention or pipe deadlock.
}

// A shared open-file-description flock survives node exit through the inherited
// helper descriptor. Close only our descriptor; LOCK_UN would also unlock the
// helper's copy. Collection holds the exclusive lock before touching any bytes.
func (p *ProcessCaller) acquireLease() (*os.File, error) {
	if p.LeasePath == "" {
		return nil, nil
	}
	fd, err := syscall.Open(p.LeasePath, syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), p.LeasePath)
	fail := func(err error) (*os.File, error) { _ = file.Close(); return nil, err }
	info, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return fail(errors.New("invalid generation lease"))
	}
	if err := syscall.Flock(fd, syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return fail(err)
	}
	named, err := os.Lstat(p.LeasePath)
	if err != nil || !os.SameFile(info, named) {
		return fail(errors.New("generation lease replaced"))
	}
	if err := p.verifyLeaseIdentity(stat); err != nil {
		return fail(err)
	}
	for _, suffix := range []string{".preparing", ".collecting", ".dropped"} {
		if _, err := os.Lstat(strings.TrimSuffix(p.LeasePath, ".lease") + suffix); !os.IsNotExist(err) {
			return fail(errors.New("generation is not finalized and usable"))
		}
	}
	if err := p.verifyFinalizedGeneration(); err != nil {
		return fail(err)
	}
	return file, nil
}

// LeaseIdentity is recorded by the installer before a generation can spawn a
// helper. Neither opener adopts a missing record or a replacement lease inode.
type LeaseIdentity struct {
	InstallationID      string `json:"installation_id"`
	Generation          uint64 `json:"generation"`
	SpecificationDigest string `json:"specification_digest"`
}

func (p *ProcessCaller) verifyLeaseIdentity(lease *syscall.Stat_t) error {
	path := strings.TrimSuffix(p.LeasePath, ".lease") + ".lease-identity"
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Size > 4096 {
		return errors.New("invalid durable generation lease identity")
	}
	var saved struct {
		LeaseIdentity
		Device uint64 `json:"device"`
		Inode  uint64 `json:"inode"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&saved) != nil || decoder.Decode(new(any)) != io.EOF || saved.LeaseIdentity != p.LeaseIdentity || saved.Generation == 0 || saved.InstallationID == "" || len(saved.SpecificationDigest) != 64 || saved.Device != uint64(lease.Dev) || saved.Inode != lease.Ino {
		return errors.New("generation lease identity differs")
	}
	named, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, named) {
		return errors.New("generation lease identity replaced")
	}
	return nil
}

// A durable lease alone is not a published provider. Initial enrollment stores
// its final config at provider.json; later generations use <generation>.json.
func (p *ProcessCaller) verifyFinalizedGeneration() error {
	path := strings.TrimSuffix(p.LeasePath, ".lease") + ".json"
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		path = filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(p.LeasePath)))), "provider.json")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Size > 16384 {
		return errors.New("invalid finalized generation")
	}
	var value struct {
		InstallationID string                 `json:"installation_id"`
		Generation     uint64                 `json:"generation"`
		Provider       string                 `json:"provider"`
		Specification  sandbox.DeploymentSpec `json:"specification"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, 16385))
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF || value.InstallationID != p.LeaseIdentity.InstallationID || value.Generation != p.LeaseIdentity.Generation || value.Provider != "microsandbox" || value.Specification.Digest(value.Provider) != p.LeaseIdentity.SpecificationDigest {
		return errors.New("finalized generation identity differs")
	}
	named, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, named) {
		return errors.New("finalized generation was replaced")
	}
	return nil
}
