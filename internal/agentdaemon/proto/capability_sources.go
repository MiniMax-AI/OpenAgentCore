package proto

import (
	"errors"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	MaxCapabilitySkills  = 50
	maxCapabilityPlugins = 50
	maxSourceDirectories = 50
)

var errInvalidCapabilitySources = errors.New("invalid capability sources")

// SkillMetadata identifies one inline Skill selection.
type SkillMetadata struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// PluginMetadata identifies one inline Plugin selection.
type PluginMetadata struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// CapabilitySources describes frozen sources; directory contents are observed after setup.
type CapabilitySources struct {
	Skills      []SkillMetadata  `json:"skills,omitempty"`
	Plugins     []PluginMetadata `json:"plugins,omitempty"`
	Directories []string         `json:"directories,omitempty"`
}

// InstalledSkill contains Runtime-owned paths, never public resource metadata.
type InstalledSkill struct {
	// InstallationRoot is resolved by Runtime and never persisted or accepted over wire.
	InstallationRoot string        `json:"-"`
	Metadata         SkillMetadata `json:"metadata"`
	RelativeRoot     string        `json:"relative_root"`
	PackageRoot      string        `json:"package_root"`
}

// PluginMCPServer is an inert Plugin package declaration. Environment variables are resolved
// from installed user configuration by Runtime, never from the parser's process.
type PluginMCPServer struct {
	Name              string            `json:"-"`
	Type              string            `json:"type,omitempty"`
	URL               string            `json:"url,omitempty"`
	BearerTokenEnvVar string            `json:"bearer_token_env_var,omitempty"`
	HTTPHeaders       map[string]string `json:"http_headers,omitempty"`
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"args,omitempty"`
	EnvVars           []string          `json:"env_vars,omitempty"`
	CWD               string            `json:"cwd,omitempty"`
}

// ValidateCapabilitySources bounds source selections without reading archives or directories.
// Portable manifest grammar is checked by the shared Skill and Plugin parsers.
func ValidateCapabilitySources(sources CapabilitySources) error {
	if len(sources.Skills) > MaxCapabilitySkills || len(sources.Plugins) > maxCapabilityPlugins || ValidateSourceDirectories(sources.Directories) != nil {
		return errInvalidCapabilitySources
	}
	validMetadata := func(kind, name, description string) bool {
		return kind == "inline" && name != "" && description != "" && utf8.ValidString(name) && utf8.ValidString(description)
	}
	for _, skill := range sources.Skills {
		if !validMetadata(skill.Type, skill.Name, skill.Description) {
			return errInvalidCapabilitySources
		}
	}
	for _, plugin := range sources.Plugins {
		if !validMetadata(plugin.Type, plugin.Name, plugin.Description) {
			return errInvalidCapabilitySources
		}
	}
	return nil
}

// ValidateSourceDirectories checks portable absolute source addresses without
// interpreting them on the Core host. Runtime authorizes and opens local paths.
func ValidateSourceDirectories(directories []string) error {
	if len(directories) > maxSourceDirectories {
		return errInvalidCapabilitySources
	}
	seen := make(map[string]bool, len(directories))
	for _, directory := range directories {
		if len(directory) == 0 || len(directory) > 4096 || !utf8.ValidString(directory) || seen[directory] || !portableAbsoluteDirectory(directory) {
			return errInvalidCapabilitySources
		}
		for _, b := range []byte(directory) {
			if b < 0x20 || b == 0x7f {
				return errInvalidCapabilitySources
			}
		}
		seen[directory] = true
	}
	return nil
}

func portableAbsoluteDirectory(value string) bool {
	// Drive letters and UNC shares are wire syntax, not a Windows implementation.
	if len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '/' || value[2] == '\\') {
		rest := strings.ReplaceAll(value[2:], "\\", "/")
		return path.Clean(rest) == rest && !strings.ContainsAny(rest, ":*?\"<>|")
	}
	if strings.HasPrefix(value, "\\\\") || strings.HasPrefix(value, "//") {
		rest := strings.ReplaceAll(value[2:], "\\", "/")
		parts := strings.Split(rest, "/")
		if len(parts) < 2 || strings.ContainsAny(rest, ":*?\"<>|") {
			return false
		}
		for _, part := range parts {
			if part == "" || part == "." || part == ".." {
				return false
			}
		}
		return true
	}
	return strings.HasPrefix(value, "/") && !strings.Contains(value, "\\") && path.Clean(value) == value
}
