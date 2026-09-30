package api

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"slices"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentnetwork"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// Validate the reference and inline shape without looking up mutable resources.
// Caller intent remains available before any reference is resolved.
func decodeTemplateEnvironment(raw json.RawMessage) (*v1.Environment, string, json.RawMessage, error) {
	return decodePreparationTemplate(raw, false)
}

func decodePreparationTemplate(raw json.RawMessage, extension bool) (*v1.Environment, string, json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, "", nil, store.ErrInvalidInput
	}
	reference, supplied := fields["environment_template_id"]
	if !supplied {
		environment, err := decodeSessionEnvironment(raw)
		var kind string
		_ = json.Unmarshal(fields["type"], &kind)
		if extension && kind != "none" {
			environment, err = decodePreparedEnvironment(raw)
		}
		return environment, "", nil, err
	}
	var id, kind string
	if json.Unmarshal(reference, &id) != nil || id == "" || json.Unmarshal(fields["type"], &kind) != nil || (kind != "openai_hosted" && !(extension && kind == "self_hosted")) {
		return nil, "", nil, store.ErrInvalidInput
	}
	delete(fields, "environment_template_id")
	inline, err := json.Marshal(fields)
	if err != nil {
		return nil, "", nil, err
	}
	environment, err := decodePreparedEnvironment(inline)
	return environment, id, raw, err
}

func (h *Handler) resolveTemplateEnvironment(ctx context.Context, tenant string, input *sessionRequest) error {
	if input.templateID == "" {
		return nil
	}
	template, files, err := h.EnvironmentTemplates.ResolveEnvironmentTemplate(ctx, tenant, input.templateID)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(input.templateEnvironment, &fields) != nil {
		return store.ErrInvalidInput
	}
	if input.Environment.Type == "self_hosted" && template.NetworkAccess != "enabled" {
		return &fieldError{param: "x_agents_core.environment.environment_template_id", message: "This template requires a managed network policy; user-managed machines do not enforce it."}
	}
	if !templateFieldOverride(fields, "network") {
		input.Environment.Network = &v1.EnvironmentNetworkInput{Access: template.NetworkAccess, AllowedDomains: append([]string{}, template.AllowedDomains...)}
	}
	effective := agentnetwork.Policy{Access: input.Environment.Network.Access, AllowedDomains: input.Environment.Network.AllowedDomains}
	if !effective.Narrows(agentnetwork.Policy{Access: template.NetworkAccess, AllowedDomains: template.AllowedDomains}) {
		return store.ErrInvalidInput
	}
	skills := input.initialization.Skills
	if !templateFieldOverride(fields, "skills") {
		skills = template.Initialization.Skills
	}
	plugins := input.initialization.Plugins
	if !templateFieldOverride(fields, "plugins") {
		plugins = template.Initialization.Plugins
	}
	directories := input.initialization.CapabilityDirectories
	if !templateFieldOverride(fields, "capability_directories") {
		directories = template.Initialization.CapabilityDirectories
	}
	setup := template.Initialization
	setup.Env = maps.Clone(setup.Env)
	if len(input.initialization.Env) > 0 {
		if setup.Env == nil {
			setup.Env = make(map[string]string)
		}
		maps.Copy(setup.Env, input.initialization.Env)
	}
	if templateFieldOverride(fields, "setup_commands") {
		setup.Commands = input.initialization.Commands
	}
	setup.Commands = slices.Clone(setup.Commands)
	if templateFieldOverride(fields, "files") {
		files = input.initialFiles
	}
	files = slices.Clone(files)
	var managers map[string]json.RawMessage
	if templateFieldOverride(fields, "packages") {
		if json.Unmarshal(fields["packages"], &managers) != nil {
			return store.ErrInvalidInput
		}
	}
	if templateFieldOverride(managers, "npm") {
		setup.Packages.NPM = input.initialization.Packages.NPM
	}
	if templateFieldOverride(managers, "python") {
		setup.Packages.Python = input.initialization.Packages.Python
	}
	setup.Packages.NPM = slices.Clone(setup.Packages.NPM)
	setup.Packages.Python = slices.Clone(setup.Packages.Python)
	setup.Skills, setup.Plugins, setup.CapabilityDirectories = skills, plugins, directories
	if err := setup.Validate(); err != nil {
		return err
	}
	if err := store.ValidateInitialFiles(files); err != nil {
		return err
	}
	input.initialization = setup
	input.Environment.Plugins = pluginResponse(input.initialization.PluginMetadata())
	input.Environment.CapabilityDirectories = append([]string{}, directories...)
	input.Environment.Skills = skillResponse(input.initialization.SkillMetadata())
	packages := setup.PackageMetadata()
	input.Environment.Packages = &packages
	input.initialFiles = files
	input.Environment.Files = initialFileResponse(files)
	// Runtime sees only the effective preparation snapshot.
	return nil
}

func templateFieldOverride(fields map[string]json.RawMessage, name string) bool {
	value, supplied := fields[name]
	return supplied && !bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}
