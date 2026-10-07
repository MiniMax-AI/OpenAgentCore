package dispatch

import (
	"context"
	"errors"
	"io"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Environment owns one Session's Environment: its resources and every effect on
// them. The Router resolves it from the Session's assignment_bind and admits,
// frames and fences each operation; the owner performs it. A Session without
// an owner declares none of these operations, and the Router rejects each with
// its typed unsupported code. docs/runtime-protocol.md defines the semantics.
type Environment interface {
	// Configure checks an execution configuration against the Environment and
	// returns it with the Environment's workspace root. It has no effects.
	Configure(proto.PromptRequestPayload) (proto.PromptRequestPayload, error)
	// Prepare fills the configured execution's installed capabilities before
	// the Executor factory runs.
	Prepare(context.Context, proto.PromptRequestPayload) (proto.PromptRequestPayload, error)
	// ApplyRuntimePreparation applies one complete runtime_prepare transfer and
	// returns only after its mutations stop.
	ApplyRuntimePreparation(context.Context, proto.RuntimePreparePayload, []byte) error
	ListWorkspaceDirectory(ctx context.Context, path string, maxEntries int) (agent.WorkspaceDirectoryResult, error)
	WriteWorkspaceFile(ctx context.Context, path string, data []byte) (agent.WorkspaceWriteResult, error)
	ExportOutputs(context.Context, io.Writer) error
	// Close releases what the owner holds for the assignment once its work and
	// Executors have settled. A retried release calls it again.
	Close(context.Context) error
}

func validateExecutionEnvironment(req proto.PromptRequestPayload, caps proto.AgentKindCapabilities) error {
	if (req.LocalEnvironment != nil) == req.DisableExecutionEnvironment {
		return errors.New("execution requires exactly one of local_environment and disable_execution_environment")
	}
	if err := req.ValidateProgrammaticToolCallingDisable(caps.ProgrammaticToolCallingDisable.IsSupported()); err != nil {
		return err
	}
	if err := req.ValidateToolSearch(caps.ToolSearch.IsSupported()); err != nil {
		return err
	}
	if req.LocalEnvironment != nil && !caps.LocalEnvironment.IsSupported() {
		return errors.New("engine does not support this local Environment configuration")
	}
	if req.DisableExecutionEnvironment && !caps.EnvironmentNone.IsSupported() {
		return errors.New("engine does not support execution environment none")
	}
	return validateMCPHTTP(req, caps)
}
