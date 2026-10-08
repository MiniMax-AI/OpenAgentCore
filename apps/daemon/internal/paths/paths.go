// Package paths resolves on-disk locations for oac-daemon state under
// ~/.oac. These functions only resolve paths; callers do the I/O.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultProfile names the directory below daemon/ that holds the
// background daemon's pid and log files.
const DefaultProfile = "default"

// Root returns ~/.oac. Honours OAC_RUNTIME_HOME for tests /
// sandbox environments without a writable home.
func Root() (string, error) {
	if override := os.Getenv("OAC_RUNTIME_HOME"); override != "" {
		if !filepath.IsAbs(override) || filepath.Clean(override) != override {
			return "", fmt.Errorf("OAC_RUNTIME_HOME must be a clean absolute directory")
		}
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".oac"), nil
}

// PIDFile returns the background daemon's connect.pid.
func PIDFile() (string, error) { return daemonFile("connect.pid") }

// LogFile returns the background daemon's connect.log.
func LogFile() (string, error) { return daemonFile("connect.log") }

func daemonFile(name string) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "daemon", DefaultProfile, name), nil
}
