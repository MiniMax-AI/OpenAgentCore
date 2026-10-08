package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// Installation supplies adapter-owned activation paths for agent-host images.
type Installation struct {
	AgentKind   string
	Environment func(directory, node string) map[string]string
}

// ManifestEnvironment reads the installation manifest at path, which an
// agent-host image carries, and returns the environment that activates each
// Harness it lists: what the Harness's Installation derives from its
// directory and Node. Discovery reads that environment. The manifest is a
// JSON object with "node", the absolute path of Node, and "harnesses", the
// absolute directory of each installed Harness by agent kind. A kind without
// an Installation in installations is an error.
func ManifestEnvironment(path string, installations ...Installation) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("installation manifest: %w", err)
	}
	var manifest struct {
		Node      string            `json:"node"`
		Harnesses map[string]string `json:"harnesses"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("installation manifest %s: %w", path, err)
	}
	if !filepath.IsAbs(manifest.Node) {
		return nil, fmt.Errorf("installation manifest %s: node %q is not an absolute path", path, manifest.Node)
	}
	env := map[string]string{}
	for kind, dir := range manifest.Harnesses {
		i := slices.IndexFunc(installations, func(i Installation) bool { return i.AgentKind == kind })
		if i < 0 || !filepath.IsAbs(dir) {
			return nil, fmt.Errorf("installation manifest %s: %s at %q is not an installation of a known kind at an absolute path", path, kind, dir)
		}
		maps.Copy(env, installations[i].Environment(dir, manifest.Node))
	}
	return env, nil
}
