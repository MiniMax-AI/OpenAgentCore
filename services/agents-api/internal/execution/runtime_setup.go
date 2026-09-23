package execution

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentbundle"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// runtimeSetupOperation is the packaged initializer's confidential stdin contract.
// Public templates and native harness configuration never cross this boundary.
type runtimeSetupOperation struct {
	Capabilities *agentcapabilities.Operation `json:"-"`
	Skill        *store.EnvironmentSkill      `json:"-"`
	Name         string                       `json:"name,omitempty"`
	Files        []agentbundle.File           `json:"files,omitempty"`
	Version      int                          `json:"version"`
	Action       string                       `json:"action"`
	Network      string                       `json:"network,omitempty"`
	Env          map[string]string            `json:"env"`
	Packages     []string                     `json:"packages,omitempty"`
	Command      string                       `json:"command,omitempty"`
	CWD          string                       `json:"cwd,omitempty"`
	// Index is the setup command position, used only for the public failure label.
	Index int `json:"-"`
}

// runtimeStepFailure is a confirmed failed initialization receipt. It holds only
// the Runtime-reported exit status (0 when absent), never command or output.
type runtimeStepFailure struct{ exitCode int }

func (*runtimeStepFailure) Error() string { return "environment initialization operation failed" }

// provisioningFailure labels a confirmed failed setup operation for the Store,
// which composes the public reason. Other actions keep the generic reason.
func (operation runtimeSetupOperation) provisioningFailure(exitCode int) store.ProvisioningFailure {
	switch operation.Action {
	case "setup", "python", "npm", "system", "skill":
		return store.ProvisioningFailure{Step: operation.Action, Index: operation.Index, ExitCode: exitCode}
	}
	return store.ProvisioningFailure{}
}

func setupOperations(setup store.EnvironmentSetup) []runtimeSetupOperation {
	if setup.Empty() {
		return nil
	}
	env := setup.Env
	if env == nil {
		env = map[string]string{}
	}
	result := []runtimeSetupOperation{{Version: 1, Action: "configure", Env: env}}
	for i := range setup.Skills {
		result = append(result, runtimeSetupOperation{Version: 1, Action: "skill", Skill: &setup.Skills[i]})
	}
	for i, plugin := range setup.Plugins {
		result = append(result, runtimeSetupOperation{Capabilities: &agentcapabilities.Operation{Version: 1, Action: "plugin", Slot: i, Archive: plugin.Archive, Plugin: plugin.Metadata}})
	}
	// The public network policy applies after setup completes. Provisioning uses
	// the isolated initializer's network; adapters enforce the runtime policy.
	const network = "enabled"
	if len(setup.Packages.System) > 0 {
		result = append(result, runtimeSetupOperation{Version: 1, Action: "system", Network: network, Packages: setup.Packages.System})
	}
	if len(setup.Packages.NPM) > 0 {
		result = append(result, runtimeSetupOperation{Version: 1, Action: "npm", Network: network, Packages: setup.Packages.NPM})
	}
	if len(setup.Packages.Python) > 0 {
		result = append(result, runtimeSetupOperation{Version: 1, Action: "python", Network: network, Packages: setup.Packages.Python})
	}
	for i, command := range setup.Commands {
		cwd := command.CWD
		if cwd == "" {
			cwd = "/workspace"
		}
		result = append(result, runtimeSetupOperation{Version: 1, Action: "setup", Network: network, Command: command.Command, CWD: cwd, Index: i})
	}
	if len(setup.Skills)+len(setup.Plugins)+len(setup.CapabilityDirectories) > 0 {
		sources := agentcapabilities.Input{Plugins: setup.PluginMetadata(), Directories: setup.CapabilityDirectories}
		for _, skill := range setup.Skills {
			sources.Skills = append(sources.Skills, skill.InstallationMetadata())
		}
		result = append(result, runtimeSetupOperation{Capabilities: &agentcapabilities.Operation{Version: 1, Action: "finalize", Sources: sources}})
	}
	return result
}

func runRuntimeSetup(ctx context.Context, provider sandbox.Provider, reference sandbox.Reference, operation runtimeSetupOperation) error {
	if provider == nil {
		return sandbox.ErrInvalid
	}
	if operation.Skill != nil {
		files, err := agentskill.Read(operation.Skill.Archive, operation.Skill.InstallationMetadata())
		if err != nil {
			return err
		}
		operation.Name, operation.Files = operation.Skill.Metadata.Name, files
	}
	var payload any = operation
	args := []string{"/usr/bin/python3", "-I", "-S", "/usr/local/bin/agents-api-runtime-initialize"}
	if operation.Capabilities != nil {
		payload = operation.Capabilities
		args = []string{"/usr/local/bin/parsar-daemon", "runtime-capabilities"}
	}
	input, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	result, err := provider.RunCommand(ctx, reference, sandbox.Command{Directory: "/", Args: args, Stdin: input})
	if err != nil {
		return err
	}
	var receipt struct {
		Version  int    `json:"version"`
		Outcome  string `json:"outcome"`
		ExitCode *int   `json:"exit_code"`
	}
	valid := result.Stderr == "" && json.Unmarshal([]byte(result.Stdout), &receipt) == nil && receipt.Version == 1
	if valid && result.ExitCode == 0 && receipt.Outcome == "completed" {
		return nil
	}
	// The initializer confirms a failed step with status 1 and, for a sandboxed
	// step, that step's exit status only. Images without exit_code stay generic.
	if valid && result.ExitCode == 1 && receipt.Outcome == "failed" && operation.Capabilities == nil {
		failure := &runtimeStepFailure{}
		if receipt.ExitCode != nil && *receipt.ExitCode > 0 && *receipt.ExitCode < 256 {
			failure.exitCode = *receipt.ExitCode
		}
		return failure
	}
	return errors.New("environment initialization operation unconfirmed")
}
