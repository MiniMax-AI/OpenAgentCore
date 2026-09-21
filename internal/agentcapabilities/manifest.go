// Package agentcapabilities owns the packaged Runtime's inert installation data.
package agentcapabilities

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentplugin"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
)

const Directory = "/environment/initialization/capabilities"
const ManifestName = "installed.json"
const MaxSkills = 50
const MaxSnapshotBytes = 50 << 20

var ErrInvalid = errors.New("capability installation unavailable or unsupported")

// InstalledSkill contains Runtime-owned paths, never public resource metadata.
type InstalledSkill struct {
	Metadata     agentskill.Metadata `json:"metadata"`
	RelativeRoot string              `json:"relative_root"`
	PackageRoot  string              `json:"package_root"`
}

type Manifest struct {
	Version int              `json:"version"`
	Skills  []InstalledSkill `json:"skills"`
}

// Input describes frozen sources; directory contents are observed after setup.
type Input struct {
	Skills      []agentskill.Metadata  `json:"skills,omitempty"`
	Plugins     []agentplugin.Metadata `json:"plugins,omitempty"`
	Directories []string               `json:"directories,omitempty"`
}

func ValidateDirectories(directories []string) error {
	if len(directories) > 50 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, directory := range directories {
		if (directory != "/workspace" && !strings.HasPrefix(directory, "/workspace/")) ||
			path.Clean(directory) != directory || strings.ContainsAny(directory, "\\\x00\r\n") || seen[directory] {
			return ErrInvalid
		}
		seen[directory] = true
	}
	return nil
}

func validRelative(value string) bool {
	return value != "." && fs.ValidPath(value) && !strings.ContainsAny(value, "\\\x00\r\n")
}

func (m *Manifest) add(metadata agentskill.Metadata, root, pkg string) error {
	if !validRelative(root) || !validRelative(pkg) || (root != pkg && !strings.HasPrefix(root, pkg+"/")) || len(m.Skills) >= MaxSkills {
		return ErrInvalid
	}
	for _, old := range m.Skills {
		if old.Metadata.Name == metadata.Name {
			return ErrInvalid
		}
	}
	m.Skills = append(m.Skills, InstalledSkill{Metadata: metadata, RelativeRoot: root, PackageRoot: pkg})
	return nil
}

func decodeManifest(body []byte) (Manifest, error) {
	var result Manifest
	if len(body) > 256<<10 || json.Unmarshal(body, &result) != nil || result.Version != 1 || len(result.Skills) > MaxSkills {
		return Manifest{}, ErrInvalid
	}
	checked := Manifest{Version: 1}
	for _, skill := range result.Skills {
		if checked.add(skill.Metadata, skill.RelativeRoot, skill.PackageRoot) != nil {
			return Manifest{}, ErrInvalid
		}
	}
	return result, nil
}
