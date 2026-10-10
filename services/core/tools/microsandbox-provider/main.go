//go:build linux

// The helper performs one finite Provider operation. Only Core owns durable
// lifecycle intent, allocation generations, scheduling and recovery policy.
package main

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

func main() {
	// The inherited node generation lease covers this helper's actual lifetime.
	// Set CLOEXEC before SDK initialization or any possible subprocess spawn so
	// long-lived VMs and host daemons cannot inherit a local helper reference.
	if os.Getenv("OAC_NODE_GENERATION_LEASE_FD") == "3" {
		syscall.CloseOnExec(3)
	}
	response := serve(os.Stdin)
	// Errors and upstream diagnostics can contain secrets: expose only stable codes.
	_ = json.NewEncoder(os.Stdout).Encode(response)
}
func serve(input io.Reader) wire.Response {
	out := wire.Response{Version: wire.ProtocolVersion}
	decoder := json.NewDecoder(io.LimitReader(input, wire.MaxRequestBytes+1))
	decoder.DisallowUnknownFields()
	var q wire.Request
	if decoder.Decode(&q) != nil || wire.ValidateRequest(q) != nil {
		out.ErrorCode = "invalid"
		return out
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		out.ErrorCode = "invalid"
		return out
	}
	if err := checkInstallation(q.Config); err != nil {
		out.ErrorCode = "unconfirmed"
		return out
	}
	lock, err := allocationLock(q)
	if err != nil {
		out.ErrorCode = "unconfirmed"
		return out
	}
	defer lock.Close()
	// Lifecycle calls deliberately retain the flock until the SDK has settled.
	// Cancelling an FFI wait does not prove the underlying operation stopped.
	b := backend{q: q}
	if q.Operation == "create" {
		if err := lock.admitCreate(); err != nil {
			out.ErrorCode = code(err)
			return out
		}
	}
	out, err = b.run(context.Background())
	if q.Operation == "initial_info" && sdk.IsKind(err, sdk.ErrSandboxNotFound) {
		out, err = lock.settleInitialAbsence(q, err)
	}
	out.Version = wire.ProtocolVersion
	if err != nil {
		out.ErrorCode = code(err)
	}
	return out
}
func code(err error) string {
	switch {
	case errors.Is(err, sandbox.ErrInvalid):
		return "invalid"
	case errors.Is(err, sandbox.ErrOwnership), sdk.IsKind(err, sdk.ErrSandboxReplaced):
		return "ownership"
	case errors.Is(err, sandbox.ErrExists), sdk.IsKind(err, sdk.ErrSandboxAlreadyExists):
		return "exists"
	case errors.Is(err, sandbox.ErrNotFound), sdk.IsKind(err, sdk.ErrSandboxNotFound):
		return "not_found"
	case errors.Is(err, sandbox.ErrCommandUnconfirmed):
		return "command_unconfirmed"
	case errors.Is(err, runtimeobs.ErrUnavailable), sdk.IsKind(err, sdk.ErrMetricsDisabled), sdk.IsKind(err, sdk.ErrMetricsUnavailable):
		return "metrics_unavailable"
	default:
		return "unconfirmed"
	}
}

type allocationGuard struct{ file *os.File }

func (g *allocationGuard) Close() {
	_ = syscall.Flock(int(g.file.Fd()), syscall.LOCK_UN)
	_ = g.file.Close()
}

// Empty admits the initial Create. The terminal marker is never removed;
// any other nonempty content also prevents creation after an incomplete write.
func (g *allocationGuard) admissionClosed() (bool, error) {
	if _, err := g.file.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	data, err := io.ReadAll(io.LimitReader(g.file, 8))
	if err != nil {
		return false, err
	}
	switch string(data) {
	case "":
		return false, nil
	case "closed\n":
		return true, nil
	default:
		return false, sandbox.ErrOwnership
	}
}

func (g *allocationGuard) admitCreate() error {
	closed, err := g.admissionClosed()
	if err != nil {
		return err
	}
	if closed {
		return sandbox.ErrInvalid
	}
	return nil
}

// Only the explicit initial observation can close admission. The caller holds
// this allocation's flock and has just received typed native absence.
func (g *allocationGuard) settleInitialAbsence(q wire.Request, nativeErr error) (wire.Response, error) {
	if q.Operation != "initial_info" || wire.ValidateRequest(q) != nil || !sdk.IsKind(nativeErr, sdk.ErrSandboxNotFound) {
		return wire.Response{}, sandbox.ErrInvalid
	}
	if _, err := g.admissionClosed(); err != nil {
		return wire.Response{}, err
	}
	if _, err := g.file.WriteAt([]byte("closed\n"), 0); err != nil {
		return wire.Response{}, err
	}
	if err := g.file.Sync(); err != nil {
		return wire.Response{}, err
	}
	// The directory entry must survive restart even when this observation was
	// the first helper to open the allocation lock.
	lockDir := filepath.Dir(g.file.Name())
	for _, path := range []string{lockDir, filepath.Dir(lockDir)} {
		dir, err := os.Open(path)
		if err != nil {
			return wire.Response{}, err
		}
		err = dir.Sync()
		_ = dir.Close()
		if err != nil {
			return wire.Response{}, err
		}
	}
	return wire.Response{State: &wire.State{Compute: q.Compute, Status: "absent"}, CreateSettled: true}, nil
}

func allocationLock(q wire.Request) (*allocationGuard, error) {
	dir := filepath.Join(q.Config.RuntimeHome, "oac-locks")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	st, e := os.Lstat(dir)
	if e != nil {
		return nil, e
	}
	if !st.IsDir() || st.Mode().Perm() != 0700 {
		return nil, sandbox.ErrOwnership
	}
	name := filepath.Join(dir, wire.Name(q.Config, q.Reference, 0)+".lock")
	fd, e := syscall.Open(name, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	for {
		if !time.Now().Before(q.Deadline) {
			f.Close()
			return nil, context.DeadlineExceeded
		}
		e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if e == nil {
			guard := &allocationGuard{file: f}
			if !time.Now().Before(q.Deadline) {
				guard.Close()
				return nil, context.DeadlineExceeded
			}
			return guard, nil
		}
		if e != syscall.EWOULDBLOCK && e != syscall.EAGAIN {
			f.Close()
			return nil, e
		}
		time.Sleep(20 * time.Millisecond)
	}
}
func checkInstallation(c wire.Config) error {
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return sandbox.ErrInvalid
	}
	matched := false
	for _, d := range build.Deps {
		if d.Path == "github.com/superradcompany/microsandbox/sdk/go" {
			matched = d.Version == wire.SDKVersion && d.Replace == nil
		}
	}
	if !matched {
		return sandbox.ErrInvalid
	}
	for _, v := range []struct{ path, hash string }{{c.RuntimePath, c.RuntimeSHA256}, {c.FirmwarePath, c.FirmwareSHA256}} {
		f, e := os.Open(v.path)
		if e != nil {
			return e
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		_ = f.Close()
		if e != nil || hex.EncodeToString(h.Sum(nil)) != v.hash {
			return sandbox.ErrInvalid
		}
	}
	if e := runtimeVersion(c.RuntimePath); e != nil {
		return e
	}
	if e := os.MkdirAll(c.RuntimeHome, 0700); e != nil {
		return e
	}
	st, e := os.Lstat(c.RuntimeHome)
	if e != nil {
		return e
	}
	if !st.IsDir() || st.Mode().Perm() != 0700 {
		return sandbox.ErrOwnership
	}
	// Explicit paths and a local backend avoid ambient cloud/profile selection.
	for _, v := range wire.HelperEnvironment(nil, c) {
		key, value, ok := cutEnv(v)
		if ok {
			if e := os.Setenv(key, value); e != nil {
				return e
			}
		}
	}
	configPath, e := isolatedConfig(c.RuntimeHome)
	if e != nil {
		return e
	}
	if e := os.Setenv("MSB_CONFIG_PATH", configPath); e != nil {
		return e
	}
	runtime, e := sdk.ResolveRuntime(sdk.RuntimeConfig{Home: c.RuntimeHome, MSBPath: c.RuntimePath, LibkrunfwPath: c.FirmwarePath})
	if e != nil {
		return e
	}
	if runtime.MSBPath != c.RuntimePath || runtime.LibkrunfwPath != c.FirmwarePath {
		return sandbox.ErrOwnership
	}
	selected, e := sdk.DefaultBackendInfo()
	if e != nil {
		return e
	}
	if selected.Kind != sdk.BackendLocal {
		return sandbox.ErrOwnership
	}
	return nil
}
func cutEnv(s string) (string, string, bool) {
	for i := range s {
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

func runtimeVersion(path string) error {
	binary, e := elf.Open(path)
	if e != nil {
		return e
	}
	defer binary.Close()
	section := binary.Section(".msbver")
	if section == nil {
		return sandbox.ErrInvalid
	}
	version, e := section.Data()
	if e != nil {
		return e
	}
	if strings.TrimRight(string(version), "\x00") != strings.TrimPrefix(wire.SDKVersion, "v") {
		return sandbox.ErrInvalid
	}
	return nil
}

// An empty JSON object prevents ambient SDK profiles without invalid empty input.
func isolatedConfig(home string) (string, error) {
	path := filepath.Join(home, "oac-sdk-config.json")
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err == nil {
		f := os.NewFile(uintptr(fd), path)
		_, err = f.WriteString("{}\n")
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
	} else if !os.IsExist(err) {
		return "", err
	}
	fd, err = syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return "", sandbox.ErrOwnership
	}
	data, err := io.ReadAll(io.LimitReader(f, 4))
	if err != nil {
		return "", err
	}
	if string(data) != "{}\n" {
		return "", sandbox.ErrOwnership
	}
	return path, nil
}
