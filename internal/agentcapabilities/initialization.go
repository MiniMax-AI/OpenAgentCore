package agentcapabilities

import (
	"encoding/json"
	"maps"
	"strings"
)

// Marker is the completion marker of identity's installation at root: its
// file name and exact contents. It lives outside the installation, so once
// it exists a missing manifest is corruption, not a reason to capture the
// sources again.
func Marker(identity Identity, root string) (string, []byte, error) {
	if validateIdentity(identity) != nil {
		return "", nil, ErrInvalid
	}
	body, err := json.Marshal(struct {
		Root string `json:"capability_root"`
	}{root})
	if err != nil {
		return "", nil, ErrInvalid
	}
	return identity.EnvironmentID + "-" + identity.SessionID + ".json", append(body, '\n'), nil
}

// ToolEnvironment is the tool environment preparation freezes: values, with
// the package commands, npmBin and pythonBin, ahead of PATH, or of path when
// values sets none, and the Python packages, pythonLib, ahead of PYTHONPATH.
// separator joins list entries.
func ToolEnvironment(values map[string]string, path, npmBin, pythonBin, pythonLib, separator string) map[string]string {
	result := make(map[string]string, len(values)+2)
	maps.Copy(result, values)
	if configured, ok := values["PATH"]; ok {
		path = configured
	}
	result["PATH"] = npmBin + separator + pythonBin + separator + path
	result["PYTHONPATH"] = pythonLib
	if values["PYTHONPATH"] != "" {
		result["PYTHONPATH"] += separator + values["PYTHONPATH"]
	}
	return result
}

// ValidToolEnvironment reports whether values are tool variables; foldCase
// rejects names that differ only in case, as Windows does.
func ValidToolEnvironment(values map[string]string, foldCase bool) bool {
	seen := map[string]bool{}
	for key, value := range values {
		if foldCase {
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

// InitializationArgs returns the arguments that follow the program of a
// setup, npm or python initialization step: Bash runs command, npm installs
// packages into npmPrefix, and Python's pip into pythonTarget.
func InitializationArgs(action, command string, packages []string, npmPrefix, pythonTarget string) ([]string, error) {
	switch action {
	case "setup":
		if command == "" || strings.ContainsRune(command, 0) {
			return nil, ErrInvalid
		}
		return []string{"--noprofile", "--norc", "-c", command}, nil
	case "npm", "python":
		if len(packages) == 0 {
			return nil, ErrInvalid
		}
		for _, value := range packages {
			if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "\x00\r\n") {
				return nil, ErrInvalid
			}
		}
		if action == "npm" {
			return append([]string{"install", "--global", "--prefix", npmPrefix, "--"}, packages...), nil
		}
		return append([]string{"-m", "pip", "install", "--disable-pip-version-check", "--no-input", "--target", pythonTarget, "--"}, packages...), nil
	}
	return nil, ErrInvalid
}
