package dispatch

import (
	"context"
	"errors"
	"fmt"
	"io"

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
	ListWorkspaceDirectory(ctx context.Context, path string, maxEntries int) (WorkspaceDirectoryResult, error)
	WriteWorkspaceFile(ctx context.Context, path string, data []byte) (WorkspaceWriteResult, error)
	ExportOutputs(context.Context, io.Writer) error
	// Close releases what the owner holds for the assignment once its work and
	// Executors have settled. A retried release calls it again.
	Close(context.Context) error
}

// InitializationFailure is a runtime_prepare step that confirmably failed. It
// carries only the step's safe exit status, when it has one.
type InitializationFailure struct{ ExitCode *int }

func (*InitializationFailure) Error() string { return "Runtime initialization failed" }

// WorkspaceDirectoryEntry describes an entry observed without following its final symlink.
type WorkspaceDirectoryEntry struct {
	Name      string
	Kind      string
	SizeBytes *int64
}

// WorkspaceDirectoryResult is a live, bounded observation, not a filesystem snapshot.
type WorkspaceDirectoryResult struct {
	Entries   []WorkspaceDirectoryEntry
	Truncated bool
}

type WorkspaceWriteResult struct {
	SizeBytes int64
}

var (
	ErrWorkspaceReadUnavailable = errors.New("workspace read unavailable")
	ErrWorkspaceReadInvalid     = errors.New("workspace read invalid")
	ErrWorkspaceReadUncertain   = errors.New("workspace read outcome uncertain")
	// ErrWorkspaceNotDirectory reports that a directory request's own path is
	// missing, a regular file or a symbolic link; the link was not followed.
	ErrWorkspaceNotDirectory = errors.New("workspace path is not a directory")

	ErrWorkspaceWriteUnavailable = errors.New("workspace write unavailable")
	ErrWorkspaceWriteBusy        = errors.New("workspace write busy")
	ErrWorkspaceWriteInvalid     = errors.New("workspace write invalid")
	ErrWorkspaceWriteRejected    = errors.New("workspace write rejected")
	ErrWorkspaceWriteUncertain   = errors.New("workspace write outcome uncertain")
)

// Known Files.create destination refusals. Both wrap ErrWorkspaceWriteRejected:
// nothing was installed.
var (
	ErrWorkspaceWriteDirectory = fmt.Errorf("%w: destination is a directory", ErrWorkspaceWriteRejected)
	ErrWorkspaceWriteUnsafe    = fmt.Errorf("%w: destination exists or its path is not a plain directory chain", ErrWorkspaceWriteRejected)
)

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
