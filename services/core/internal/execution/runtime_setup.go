package execution

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

// runtimeSetupOperation carries one frozen, typed Runtime operation. Runtime owns
// host paths, executable selection and native installation receipts.
type runtimeSetupOperation struct {
	Request proto.RuntimePreparePayload
	Data    []byte
	Index   int
}

type runtimeStepFailure struct{ exitCode int }

func (*runtimeStepFailure) Error() string { return "environment initialization operation failed" }

func (operation runtimeSetupOperation) provisioningFailure(exitCode int) store.ProvisioningFailure {
	action := operation.Request.Action
	if operation.Request.Initialization != nil {
		action = operation.Request.Initialization.Action
	}
	switch action {
	case "setup", "python", "npm", "skill":
		return store.ProvisioningFailure{Step: action, Index: operation.Index, ExitCode: exitCode}
	}
	return store.ProvisioningFailure{}
}

func setupOperations(setup store.EnvironmentSetup) []runtimeSetupOperation {
	if setup.Empty() {
		return nil
	}
	initialize := func(input proto.RuntimeInitialization) runtimeSetupOperation {
		return runtimeSetupOperation{Request: proto.RuntimePreparePayload{Action: "initialize", Initialization: &input}}
	}
	env := setup.Env
	if env == nil {
		env = map[string]string{}
	}
	result := []runtimeSetupOperation{initialize(proto.RuntimeInitialization{Action: "configure", Env: env})}
	for _, skill := range setup.Skills {
		metadata := skill.InstallationMetadata()
		result = append(result, runtimeSetupOperation{Request: proto.RuntimePreparePayload{Action: "skill", Skill: &metadata}, Data: skill.Archive})
	}
	for i, plugin := range setup.Plugins {
		result = append(result, runtimeSetupOperation{Request: proto.RuntimePreparePayload{Action: "plugin", Slot: i, Plugin: &plugin.Metadata}, Data: plugin.Archive})
	}
	// Provisioning network policy is distinct from the policy enforced for Turns.
	for _, packages := range []struct {
		action string
		values []string
	}{{"npm", setup.Packages.NPM}, {"python", setup.Packages.Python}} {
		if len(packages.values) > 0 {
			result = append(result, initialize(proto.RuntimeInitialization{Action: packages.action, Network: "enabled", Packages: packages.values}))
		}
	}
	for i, command := range setup.Commands {
		operation := initialize(proto.RuntimeInitialization{Action: "setup", Network: "enabled", Command: command.Command, CWD: command.CWD})
		operation.Index = i
		result = append(result, operation)
	}
	if len(setup.Skills)+len(setup.Plugins)+len(setup.CapabilityDirectories) > 0 {
		sources := agentcapabilities.Input{Plugins: setup.PluginMetadata(), Directories: setup.CapabilityDirectories}
		for _, skill := range setup.Skills {
			sources.Skills = append(sources.Skills, skill.InstallationMetadata())
		}
		result = append(result, runtimeSetupOperation{Request: proto.RuntimePreparePayload{Action: "finalize", Sources: &sources}})
	}
	return result
}

type runtimePreparer interface {
	PrepareRuntime(context.Context, string, proto.RuntimePreparePayload, []byte) (proto.RuntimePrepareResultPayload, error)
}

func runRuntimeSetup(ctx context.Context, peer runtimePreparer, identity agentcapabilities.Identity, operation runtimeSetupOperation) error {
	if peer == nil {
		return errors.New("environment initialization request unavailable")
	}
	request := operation.Request
	request.EnvironmentID, request.SessionID = identity.EnvironmentID, identity.SessionID
	result, err := peer.PrepareRuntime(ctx, uuid.NewString(), request, operation.Data)
	if err == nil && result.Outcome == "completed" {
		return nil
	}
	if err == nil && (result.Outcome == "rejected" || result.Outcome == "failed") {
		return &runtimeStepFailure{exitCode: result.ExitCode}
	}
	return errors.New("environment initialization operation unconfirmed")
}

func installInitialFile(ctx context.Context, peer runtimePreparer, identity agentcapabilities.Identity, file store.InitialFileMetadata, body []byte) error {
	if file.SizeBytes == nil || *file.SizeBytes != int64(len(body)) || len(body) > store.MaxInitialFileBytes {
		return errors.New("environment initialization request unavailable")
	}
	return runRuntimeSetup(ctx, peer, identity, runtimeSetupOperation{Request: proto.RuntimePreparePayload{Action: "file", File: &proto.RuntimeInitialFile{Path: file.Path}}, Data: body})
}
