package api

import (
	"bytes"
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

var errSystemPackages = &fieldError{
	param:   "packages.system",
	message: "Runtime system package installation is not supported. Preinstall system dependencies in the sandbox image or template, or on the host machine.",
}

func rejectSystemPackages(raw json.RawMessage) error {
	var packages map[string]json.RawMessage
	if json.Unmarshal(raw, &packages) != nil {
		return nil
	}
	// The official schema reads back `system: []`, so null and an empty list mean none.
	value, supplied := packages["system"]
	if !supplied {
		return nil
	}
	var system []json.RawMessage
	if json.Unmarshal(value, &system) != nil || len(system) != 0 {
		return errSystemPackages
	}
	return nil
}

func isNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// rejectUnsupportedEnvironmentFields reports the official Environment fields Core cannot honor.
func rejectUnsupportedEnvironmentFields(fields map[string]json.RawMessage) error {
	for _, name := range []string{"environment_id", "container_size"} {
		if !isNull(fields[name]) {
			return &fieldError{param: name, message: "Environment " + name + " is not supported."}
		}
	}
	if desktop := fields["desktop"]; !isNull(desktop) {
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if json.Unmarshal(desktop, &input) == nil && input.Enabled != nil && *input.Enabled {
			return &fieldError{param: "desktop.enabled", message: "Desktop environments are not supported."}
		}
	}
	if network := fields["network"]; !isNull(network) {
		var input struct {
			BlockedDomains []json.RawMessage `json:"blocked_domains"`
		}
		if json.Unmarshal(network, &input) == nil && len(input.BlockedDomains) != 0 {
			return &fieldError{param: "network.blocked_domains", message: "Blocked domains are not supported. Use restricted access with allowed_domains."}
		}
	}
	return nil
}

func decodeEnvironmentSetup(fields map[string]json.RawMessage) (environmentconfig.Setup, error) {
	var result environmentconfig.Setup
	if err := rejectUnsupportedEnvironmentFields(fields); err != nil {
		return result, err
	}
	if err := rejectSystemPackages(fields["packages"]); err != nil {
		return result, err
	}
	if value, ok := fields["env"]; ok {
		var entries map[string]*string
		if json.Unmarshal(value, &entries) != nil {
			return result, sessions.ErrInvalidInput
		}
		result.Env = make(map[string]string, len(entries))
		for name, entry := range entries {
			if entry == nil {
				return result, sessions.ErrInvalidInput
			}
			result.Env[name] = *entry
		}
	}
	if value, ok := fields["setup_commands"]; ok {
		var commands []json.RawMessage
		if json.Unmarshal(value, &commands) != nil {
			return result, sessions.ErrInvalidInput
		}
		for _, command := range commands {
			var input struct {
				Command *string `json:"command"`
				CWD     *string `json:"cwd"`
			}
			if decodeInputObject(command, &input, "command", "cwd") != nil || input.Command == nil {
				return result, sessions.ErrInvalidInput
			}
			step := environmentconfig.SetupCommand{Command: *input.Command}
			if input.CWD != nil {
				if *input.CWD == "" {
					return result, sessions.ErrInvalidInput
				}
				step.CWD = *input.CWD
			}
			result.Commands = append(result.Commands, step)
		}
	}
	if value, ok := fields["packages"]; ok && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		var input struct {
			NPM    []*string       `json:"npm"`
			Python []*string       `json:"python"`
			System json.RawMessage `json:"system"` // checked by rejectSystemPackages
		}
		if decodeInputObject(value, &input, "npm", "python", "system") != nil {
			return result, sessions.ErrInvalidInput
		}
		for name, entries := range map[string][]*string{"npm": input.NPM, "python": input.Python} {
			var target *[]string
			switch name {
			case "npm":
				target = &result.Packages.NPM
			case "python":
				target = &result.Packages.Python
			}
			for _, entry := range entries {
				if entry == nil {
					return result, sessions.ErrInvalidInput
				}
				*target = append(*target, *entry)
			}
		}
	}
	var err error
	result.Skills, err = decodeEnvironmentSkills(fields["skills"])
	if err != nil {
		return result, err
	}
	result.Plugins, err = decodeEnvironmentPlugins(fields["plugins"])
	if err != nil {
		return result, err
	}
	if raw, supplied := fields["capability_directories"]; supplied {
		var entries []*string
		if json.Unmarshal(raw, &entries) != nil {
			return result, sessions.ErrInvalidInput
		}
		for _, entry := range entries {
			if entry == nil {
				return result, sessions.ErrInvalidInput
			}
			result.CapabilityDirectories = append(result.CapabilityDirectories, *entry)
		}
	}
	return result, result.Validate()
}

func packageMetadata(packages *v1.EnvironmentPackages) v1.EnvironmentPackagesResponse {
	value := environmentconfig.Setup{}
	if packages != nil {
		value.Packages = *packages
	}
	normalized := value.PackageMetadata()
	return v1.EnvironmentPackagesResponse{NPM: normalized.NPM, Python: normalized.Python, System: []string{}}
}
