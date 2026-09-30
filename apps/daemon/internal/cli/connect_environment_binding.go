package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
)

// This receipt contains identity only; executor credentials remain in their file.
type environmentBinding struct {
	RemoteURL           string                `json:"remote_url"`
	Enrollment          environmentEnrollment `json:"enrollment"`
	LocalWorkspace      string                `json:"local_workspace"`
	CapabilityDirectory string                `json:"capability_directory"`
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
	return runtimefs.ReadPrivatePath(path, 16*1024)
}

func bindEnvironmentRuntime(remote string, bound environmentEnrollment, credentialFile string) error {
	root, err := paths.Root()
	if err != nil || !filepath.IsAbs(root) {
		return errors.New("connect: absolute Runtime state directory required")
	}
	if err = runtimefs.EnsurePrivateDir(root); err != nil {
		return errors.New("connect: Runtime state directory unavailable")
	}
	workspace := os.Getenv("OAC_RUNTIME_WORKSPACE")
	if workspace == "/" || agentcapabilities.ValidateLocalDirectories([]string{workspace}) != nil {
		return errors.New("connect: clean absolute Runtime workspace required")
	}
	info, statErr := os.Stat(workspace)
	if statErr != nil || !info.IsDir() {
		return errors.New("connect: existing Runtime workspace required")
	}
	if bound.WorkspaceDirectory != "/workspace" && bound.WorkspaceDirectory != workspace {
		return errors.New("connect: enrollment does not match the Runtime workspace")
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
	capabilityDirectory := os.Getenv("OAC_RUNTIME_CAPABILITY_DIRECTORY")
	if capabilityDirectory == "" {
		capabilityDirectory = localworkspace.CapabilityDirectory
	}
	if _, err := localworkspace.NewWithCapabilityDirectory(bound.EnvironmentID, bound.SessionID, workspace, capabilityDirectory); err != nil {
		return errors.New("connect: local Runtime layout unavailable")
	}
	want := environmentBinding{RemoteURL: remote, Enrollment: bound, LocalWorkspace: workspace, CapabilityDirectory: capabilityDirectory}
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
		return errors.New("connect: Runtime binding unavailable")
	}
	return nil
}

// Neither selecting private state nor selecting its parent grants workspace access.
func environmentPathsOverlap(first, second string) bool {
	for _, pair := range [][2]string{{first, second}, {second, first}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err == nil && (relative == "." || filepath.IsLocal(relative)) {
			return true
		}
	}
	return false
}

func saveEnvironmentBinding(root string, want environmentBinding) error {
	// Store the binding with the other daemon state.
	dir := filepath.Join(root, "daemon")
	if err := runtimefs.EnsurePrivateDir(dir); err != nil {
		return errors.New("connect: Runtime state directory unavailable")
	}
	for _, path := range []string{root, dir} {
		held, err := os.OpenRoot(path)
		if err != nil {
			return errors.New("connect: Runtime state directory unavailable")
		}
		err = runtimefs.PrivateDirectory(held)
		held.Close()
		if err != nil {
			return errors.New("connect: Runtime state directory must be private and owned")
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
		held, e := os.OpenRoot(dir)
		if e != nil {
			return errors.New("connect: Runtime binding directory unavailable")
		}
		defer held.Close()
		f, e := runtimefs.OpenPrivate(held, "environment.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if errors.Is(e, os.ErrExist) {
			return saveEnvironmentBinding(root, want)
		}
		if e != nil {
			return errors.New("connect: could not establish Runtime binding")
		}
		_, e = f.Write(data)
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
