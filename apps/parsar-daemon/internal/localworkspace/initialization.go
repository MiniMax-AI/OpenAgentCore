package localworkspace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
	"github.com/MiniMax-AI-Dev/parsar/internal/runtimefs"
)

// InitializationDirectory resolves the operator's local resource layout. The
// protocol and preparation operations do not distinguish Environment sources.
func InitializationDirectory() (string, error) {
	return initializationPath("OAC_RUNTIME_INITIALIZATION_DIRECTORY", "initialization")
}
func PackageDirectory() (string, error) {
	return initializationPath("OAC_RUNTIME_PACKAGE_DIRECTORY", "packages")
}
func initializationPath(setting, name string) (string, error) {
	if path := os.Getenv(setting); path != "" {
		if runtimefs.ValidateLocalPath(path) != nil {
			return "", errors.New("invalid Runtime initialization directory")
		}
		return path, nil
	}
	root, err := paths.Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

// ReadToolEnvironment reads bounded explicit configuration, never ambient secrets.
func ReadToolEnvironment() (map[string]string, error) {
	path, err := toolEnvironmentPath()
	if err != nil {
		return nil, err
	}
	return readToolEnvironmentFile(path)
}

func readToolEnvironmentFile(path string) (map[string]string, error) {
	body, err := runtimefs.ReadPrivatePath(path, 1<<20)
	var values map[string]string
	if err != nil || json.Unmarshal(body, &values) != nil || values == nil || !validToolEnvironment(values) {
		return nil, errors.New("initialized user environment unavailable")
	}
	return values, nil
}
func validToolEnvironment(values map[string]string) bool {
	seen := map[string]bool{}
	for key, value := range values {
		if runtime.GOOS == "windows" {
			folded := strings.ToUpper(key)
			if seen[folded] {
				return false
			}
			seen[folded] = true
		}
		if key == "" || strings.ContainsAny(key, "=\x00\r\n") || strings.ContainsRune(value, 0) {
			return false
		}
	}
	return true
}

// ReadOptionalToolEnvironment permits a Runtime without explicit user variables.
func ReadOptionalToolEnvironment() (map[string]string, error) {
	values, _, err := readOptionalToolEnvironment()
	return values, err
}

// ToolEnvironmentFile returns the validated Runtime-owned source without copying
// its values into a Harness profile. An unconfigured environment has no file.
func ToolEnvironmentFile() (string, error) {
	_, path, err := readOptionalToolEnvironment()
	return path, err
}

func readOptionalToolEnvironment() (map[string]string, string, error) {
	path, err := toolEnvironmentPath()
	if err != nil {
		return nil, "", err
	}
	if _, err = os.Stat(path); errors.Is(err, os.ErrNotExist) && os.Getenv("OAC_RUNTIME_TOOL_ENV_FILE") == "" {
		return map[string]string{}, "", nil
	}
	values, err := readToolEnvironmentFile(path)
	if err != nil {
		return nil, "", err
	}
	return values, path, nil
}

// The prepared snapshot takes precedence over the operator's source file.
// Changing that source after preparation must not change an existing Session.
func toolEnvironmentPath() (string, error) {
	path, err := initializedToolEnvironmentPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return path, err
	}
	if source := os.Getenv("OAC_RUNTIME_TOOL_ENV_FILE"); source != "" {
		if runtimefs.ValidateLocalPath(source) != nil {
			return "", errors.New("invalid Runtime tool environment file")
		}
		return source, nil
	}
	return path, nil
}

func initializedToolEnvironmentPath() (string, error) {
	directory, err := InitializationDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "tool-env.json"), nil
}

// Freeze local defaults even when Core has no setup operations to send. Once
// the capability snapshot is complete, a missing environment is corruption.
func prepareToolEnvironment(required, completed bool) error {
	path, err := initializedToolEnvironmentPath()
	if err != nil {
		return err
	}
	if _, err = os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if required || completed {
			return errors.New("prepared tool environment unavailable")
		}
		if err := configureRuntime(nil); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	_, err = readToolEnvironmentFile(path)
	return err
}
