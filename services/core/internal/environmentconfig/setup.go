package environmentconfig

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
)

// Setup is confidential input, never ordinary resource metadata.
// Core freezes it once; the common Runtime initializer executes it in order.
type Setup struct {
	Env                   map[string]string      `json:"env,omitempty"`
	Commands              []SetupCommand         `json:"setup_commands,omitempty"`
	Packages              v1.EnvironmentPackages `json:"packages"`
	Skills                []Skill                `json:"skills,omitempty"`
	Plugins               []Plugin               `json:"plugins,omitempty"`
	CapabilityDirectories []string               `json:"capability_directories,omitempty"`
}

type SetupCommand struct {
	Command string `json:"command"`
	CWD     string `json:"cwd,omitempty"`
}

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (s Setup) Empty() bool {
	return len(s.Env)+len(s.Commands)+len(s.Packages.NPM)+len(s.Packages.Python)+len(s.Skills)+len(s.Plugins)+len(s.CapabilityDirectories) == 0
}

// Validate checks requested configuration, where a Skill may still be an
// unresolved reference.
func (s Setup) Validate() error {
	return s.validate(false)
}

// ValidateInstalled checks installable configuration: every Skill carries its
// concrete metadata and frozen archive.
func (s Setup) ValidateInstalled() error {
	return s.validate(true)
}

func (s Setup) validate(installed bool) error {
	if validateSkills(s.Skills, installed) != nil || ValidatePlugins(s.Plugins) != nil || agentcapabilities.ValidateSourceDirectories(s.CapabilityDirectories) != nil {
		return ErrInvalid
	}
	ordinary := s
	ordinary.Skills = nil
	ordinary.Plugins = nil
	raw, err := json.Marshal(ordinary)
	if err != nil || len(raw) > 512*1024 {
		return ErrInvalid
	}
	for name, value := range s.Env {
		// The first three reservations are explicitly part of the public guide;
		// OAC_* identifies the actual deployment authority and binding.
		if !environmentName.MatchString(name) || name == "PATH" || name == "OPENAI_API_KEY" || strings.HasPrefix(name, "CODEX_") || strings.HasPrefix(name, "OAC_") || strings.ContainsRune(value, 0) {
			return ErrInvalid
		}
	}
	for _, command := range s.Commands {
		if command.Command == "" || strings.ContainsRune(command.Command, 0) || (command.CWD != "" && (!path.IsAbs(command.CWD) || strings.ContainsRune(command.CWD, 0))) {
			return ErrInvalid
		}
	}
	for _, packages := range [][]string{s.Packages.NPM, s.Packages.Python} {
		for _, item := range packages {
			if item == "" || strings.HasPrefix(item, "-") || strings.ContainsRune(item, 0) {
				return ErrInvalid
			}
		}
	}
	return nil
}

// PackageMetadata returns the public package lists, empty rather than null.
func (s Setup) PackageMetadata() v1.EnvironmentPackages {
	result := s.Packages
	if result.NPM == nil {
		result.NPM = []string{}
	}
	if result.Python == nil {
		result.Python = []string{}
	}
	return result
}
