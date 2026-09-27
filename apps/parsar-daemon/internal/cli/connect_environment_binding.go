package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

// This receipt contains identity only; executor credentials remain in their file.
type environmentBinding struct {
	RemoteURL      string                `json:"remote_url"`
	Enrollment     environmentEnrollment `json:"enrollment"`
	LocalWorkspace string                `json:"local_workspace"`
}

// Check persisted ownership before transmitting the executor credential.
func checkEnvironmentTarget(remote, environment string) error {
	root, err := paths.Root()
	if err != nil {
		return errors.New("connect: Runtime state unavailable")
	}
	raw, err := readEnvironmentPrivateFile(filepath.Join(root, "daemon", "environment.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var prior environmentBinding
	if err != nil || decodeEnvironmentJSON(raw, &prior) != nil || prior.RemoteURL != remote || prior.Enrollment.EnvironmentID != environment {
		return errors.New("connect: Runtime belongs to a different Environment or history")
	}
	return nil
}

func readEnvironmentPrivateFile(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("absolute private file required")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || st.Uid != uint32(os.Getuid()) || st.Nlink != 1 {
		return nil, errors.New("private owned regular file required")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 16*1024+1))
	if err != nil || len(raw) > 16*1024 {
		return nil, errors.New("private file exceeds limit")
	}
	return raw, nil
}

func bindEnvironmentRuntime(remote string, bound environmentEnrollment, credentialFile string) error {
	root, err := paths.Root()
	if err != nil || !filepath.IsAbs(root) {
		return errors.New("connect: absolute Runtime state directory required")
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return errors.New("connect: Runtime state directory unavailable")
	}
	workspace := os.Getenv("OAC_RUNTIME_WORKSPACE")
	if workspace != "/environment/workspace" {
		return errors.New("connect: packaged /workspace Runtime required")
	}
	for key, value := range map[string]string{
		"OAC_RUNTIME_ENVIRONMENT_ID": bound.EnvironmentID,
		"OAC_RUNTIME_SESSION_ID":     bound.SessionID,
		"OAC_RUNTIME_NETWORK_ACCESS": "enabled",
	} {
		if previous := os.Getenv(key); previous != "" && previous != value {
			return errors.New("connect: conflicting Runtime identity or policy")
		}
	}
	if domains := os.Getenv("OAC_RUNTIME_ALLOWED_DOMAINS"); domains != "" && domains != "[]" {
		return errors.New("connect: conflicting Runtime network domains")
	}
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return errors.New("connect: Runtime state directory unavailable")
	}
	for _, path := range []string{resolvedRoot, credentialFile} {
		resolved, e := filepath.EvalSymlinks(path)
		if e != nil {
			return errors.New("connect: private Runtime path unavailable")
		}
		for _, public := range []string{"/workspace", workspace} {
			if relative, e := filepath.Rel(public, resolved); e == nil && (relative == "." || filepath.IsLocal(relative)) {
				return errors.New("connect: private Runtime state cannot be inside the workspace")
			}
		}
	}
	private, err := filepath.EvalSymlinks(filepath.Join(root, "daemon"))
	credentialPath, credentialErr := filepath.EvalSymlinks(credentialFile)
	if err != nil || credentialErr != nil {
		return errors.New("connect: protected daemon credential directory required")
	}
	relative, err := filepath.Rel(private, credentialPath)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return errors.New("connect: executor credential must be inside the protected daemon directory")
	}
	want := environmentBinding{RemoteURL: remote, Enrollment: bound, LocalWorkspace: workspace}
	if err = saveEnvironmentBinding(root, want); err != nil {
		return err
	}
	for key, value := range map[string]string{"OAC_RUNTIME_ENVIRONMENT_ID": bound.EnvironmentID, "OAC_RUNTIME_SESSION_ID": bound.SessionID, "OAC_RUNTIME_NETWORK_ACCESS": "enabled"} {
		if err = os.Setenv(key, value); err != nil {
			return errors.New("connect: Runtime identity configuration failed")
		}
	}
	local, err := localworkspace.Load()
	if err != nil || local == nil {
		return errors.New("connect: packaged Runtime binding or helpers unavailable")
	}
	return nil
}

func saveEnvironmentBinding(root string, want environmentBinding) error {
	// All three native sandboxes protect this existing daemon state directory.
	dir := filepath.Join(root, "daemon")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("connect: Runtime state directory unavailable")
	}
	for _, path := range []string{root, dir} {
		info, e := os.Lstat(path)
		if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return errors.New("connect: Runtime state directory must be private")
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != uint32(os.Getuid()) {
			return errors.New("connect: Runtime state directory must be owned")
		}
	}
	path := filepath.Join(dir, "environment.json")
	raw, err := readEnvironmentPrivateFile(path)
	if err == nil {
		var prior environmentBinding
		if decodeEnvironmentJSON(raw, &prior) != nil || prior != want {
			return errors.New("connect: Runtime belongs to a different Environment or history")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		// Unlabelled native state cannot safely be adopted by a new enrollment.
		for _, native := range []string{"runtime", "sessions", "daemon/agent-sessions"} {
			if _, e := os.Lstat(filepath.Join(root, native)); !errors.Is(e, os.ErrNotExist) {
				return errors.New("connect: existing Runtime history has no Environment binding")
			}
		}
		profiles, e := os.ReadDir(dir)
		if e != nil {
			return errors.New("connect: Runtime state directory unavailable")
		}
		for _, entry := range profiles {
			if entry.IsDir() {
				for _, name := range []string{"auth.json", "sessions.json", "runtime"} {
					if _, e := os.Lstat(filepath.Join(dir, entry.Name(), name)); !errors.Is(e, os.ErrNotExist) {
						return errors.New("connect: existing profile has no Environment binding")
					}
				}
			}
		}
		data, _ := json.Marshal(want)
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
		if errors.Is(e, os.ErrExist) {
			return saveEnvironmentBinding(root, want)
		}
		if e != nil {
			return errors.New("connect: could not establish Runtime binding")
		}
		_, e = io.Copy(f, bytes.NewReader(data))
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			return errors.New("connect: Runtime binding write failed")
		}
	} else {
		return errors.New("connect: Runtime binding unavailable")
	}
	return nil
}
