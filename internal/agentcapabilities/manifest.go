// Package agentcapabilities owns the packaged Runtime's inert installation data.
package agentcapabilities

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimebootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"

	"github.com/google/uuid"
)

const Directory = "/environment/initialization/capabilities"
const ManifestName = "installed.json"
const MaxSkills = 50
const MaxSnapshotBytes = 50 << 20

var ErrInvalid = errors.New("capability installation unavailable or unsupported")

// Identity binds an installation to one immutable Environment and Session.
type Identity struct {
	EnvironmentID string `json:"environment_id"`
	SessionID     string `json:"session_id"`
}

// DirectoryResolver opens a declared source after local authorization and path
// checks. Finalize owns and closes each returned root; callers retain no handle.
type DirectoryResolver func(string) (*os.Root, error)

type Manifest struct {
	Identity        Identity               `json:"identity"`
	SelectionSHA256 string                 `json:"selection_sha256"`
	Version         int                    `json:"version"`
	Skills          []proto.InstalledSkill `json:"skills"`
	Plugins         []string               `json:"plugins,omitempty"`
	MCP             []InstalledMCP         `json:"-"`
}

// InstalledMCP is resolved from a frozen package at load time. The manifest
// stores the package root, not a second copy of configuration or credentials.
type InstalledMCP struct {
	PackageRoot string
	Server      proto.PluginMCPServer
}

// ValidateDirectories preserves the managed public workspace-path contract.
func ValidateDirectories(directories []string) error {
	if ValidateLocalDirectories(directories) != nil {
		return ErrInvalid
	}
	for _, directory := range directories {
		if directory != runtimebootstrap.PublicWorkspace && !strings.HasPrefix(directory, runtimebootstrap.PublicWorkspace+"/") {
			return ErrInvalid
		}
	}
	return nil
}

// ValidateLocalDirectories checks source spelling, not local access authority.
// Runtime requires canonical paths for its own operating system. The resolver
// owns directory access and protected-root exclusions.
func ValidateLocalDirectories(directories []string) error {
	if len(directories) > 50 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, directory := range directories {
		if runtimefs.ValidateLocalPath(directory) != nil || len(directory) > 4096 || !utf8.ValidString(directory) || seen[directory] {
			return ErrInvalid
		}
		seen[directory] = true
	}
	return nil
}

func validRelative(value string) bool {
	return value != "." && fs.ValidPath(value) && !strings.ContainsAny(value, "\\\x00\r\n")
}

func (m *Manifest) add(metadata proto.SkillMetadata, root, pkg string) error {
	if !validRelative(root) || !validRelative(pkg) || (root != pkg && !strings.HasPrefix(root, pkg+"/")) || len(m.Skills) >= MaxSkills {
		return ErrInvalid
	}
	for _, old := range m.Skills {
		if old.Metadata.Name == metadata.Name {
			return ErrInvalid
		}
	}
	m.Skills = append(m.Skills, proto.InstalledSkill{Metadata: metadata, RelativeRoot: root, PackageRoot: pkg})
	return nil
}

func decodeManifest(body []byte) (Manifest, error) {
	var result Manifest
	if len(body) > 256<<10 || json.Unmarshal(body, &result) != nil || result.Version != 1 || validateIdentity(result.Identity) != nil || !validSelectionHash(result.SelectionSHA256) || len(result.Skills) > MaxSkills {
		return Manifest{}, ErrInvalid
	}
	checked := Manifest{Version: 1}
	for _, skill := range result.Skills {
		if checked.add(skill.Metadata, skill.RelativeRoot, skill.PackageRoot) != nil {
			return Manifest{}, ErrInvalid
		}
	}
	for _, root := range result.Plugins {
		if checked.addMCPPackage(root) != nil {
			return Manifest{}, ErrInvalid
		}
	}
	return result, nil
}

func (m *Manifest) addMCPPackage(root string) error {
	if !validRelative(root) || len(m.Plugins) >= 100 {
		return ErrInvalid
	}
	for _, old := range m.Plugins {
		if old == root {
			return ErrInvalid
		}
	}
	m.Plugins = append(m.Plugins, root)
	return nil
}

func validateIdentity(identity Identity) error {
	for _, value := range []string{identity.EnvironmentID, identity.SessionID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return ErrInvalid
		}
	}
	return nil
}

func validSelectionHash(value string) bool {
	digest, err := hex.DecodeString(value)
	return err == nil && len(digest) == sha256.Size && hex.EncodeToString(digest) == value
}

func selectionHash(input proto.CapabilitySources) (string, error) {
	if proto.ValidateCapabilitySources(input) != nil {
		return "", ErrInvalid
	}
	// omitempty gives nil and empty selections the same canonical representation;
	// list order remains significant because it selects installed package slots.
	body, err := json.Marshal(input)
	if err != nil {
		return "", ErrInvalid
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

// ValidateSelection checks reconnect configuration without revisiting sources.
// Use it only with a manifest returned by Load.
func ValidateSelection(manifest Manifest, input proto.CapabilitySources, identity Identity) error {
	digest, err := selectionHash(input)
	if err != nil || validateIdentity(identity) != nil || manifest.Version != 1 || manifest.Identity != identity || manifest.SelectionSHA256 != digest {
		return ErrInvalid
	}
	return nil
}
