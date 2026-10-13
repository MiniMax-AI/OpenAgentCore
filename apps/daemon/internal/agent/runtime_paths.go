package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

// StateDir returns an adapter-owned state directory scoped to one agent
// state. It never derives runtime state from the subprocess cwd.
func StateDir(root, agentKind, agentStateKey string) (string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", fmt.Errorf("agent: state root must be a clean absolute directory")
	}
	kind := safeRuntimePathPart(agentKind)
	if kind == "" {
		return "", fmt.Errorf("agent: invalid agent kind %q", agentKind)
	}
	parts := safeRuntimePathParts(agentStateKey)
	if len(parts) == 0 {
		return "", fmt.Errorf("agent: invalid agent state key %q", agentStateKey)
	}
	return filepath.Join(append([]string{root, "runtime", kind, "state"}, parts...)...), nil
}

func safeRuntimePathParts(value string) []string {
	raw := strings.Split(value, "/")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		if safe := safeRuntimePathPart(part); safe != "" {
			parts = append(parts, safe)
		}
	}
	return parts
}

func safeRuntimePathPart(value string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	value = b.String()
	if value == "." || value == ".." {
		return ""
	}
	return value
}
