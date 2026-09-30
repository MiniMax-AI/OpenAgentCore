package daemonize

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
	"os"
	"path/filepath"
	"time"
)

var ErrStaleOrCorrupt = errors.New("daemonize: unsupported, stale or corrupt process record")
var ErrNotRunning = errors.New("daemonize: process not running")

type processIdentity struct {
	PID       int    `json:"pid"`
	Start     string `json:"start"`
	StopEvent string `json:"stop_event,omitempty"`
}

func WritePIDFile(path string, pid int) error {
	identity, err := identifyProcess(pid)
	if err != nil {
		return err
	}
	identity.StopEvent = os.Getenv(stopEventEnv)
	return writeIdentity(path, identity)
}

func writeIdentity(path string, identity processIdentity) error {
	if path == "" {
		return errors.New("daemonize: process record path required")
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	return runtimefs.WritePrivateAtomic(root, filepath.Base(path), raw)
}

func readIdentity(path string) (processIdentity, error) {
	raw, err := runtimefs.ReadPrivatePath(path, 4096)
	if err != nil {
		return processIdentity{}, err
	}
	var value processIdentity
	if json.Unmarshal(raw, &value) != nil || value.PID <= 0 || value.Start == "" {
		return processIdentity{}, ErrStaleOrCorrupt
	}
	actual, err := identifyProcess(value.PID)
	if err != nil {
		if errors.Is(err, ErrNotRunning) {
			return processIdentity{}, fmt.Errorf("%w: %w", ErrStaleOrCorrupt, err)
		}
		return processIdentity{}, err
	}
	if actual.Start != value.Start {
		return processIdentity{}, ErrStaleOrCorrupt
	}
	return value, nil
}

func ReadPIDFile(path string) (int, error) {
	identity, err := readIdentity(path)
	return identity.PID, err
}

func IsAlive(pid int) error { _, err := identifyProcess(pid); return err }

// StopPIDFile never removes ownership before the exact process has exited.
// A cleanup timeout leaves the record available for observation and a later stop.
func StopPIDFile(path string, timeout time.Duration) error {
	identity, err := readIdentity(path)
	if err != nil {
		return err
	}
	if err = stopProcess(identity, timeout); err != nil {
		return err
	}
	raw, err := runtimefs.ReadPrivatePath(path, 4096)
	if err != nil {
		return err
	}
	var current processIdentity
	if json.Unmarshal(raw, &current) != nil || current != identity {
		return errors.New("daemonize: process ownership changed during stop")
	}
	return RemovePIDFile(path)
}

func RemovePIDFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
