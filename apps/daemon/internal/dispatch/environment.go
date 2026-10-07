package dispatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
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
	// ApplyRuntimePreparation applies one complete runtime_prepare transfer,
	// whose envelope ID is transfer, and returns only after its mutations
	// stop. Core never sends a transfer ID twice, so transfer may name the
	// effects the transfer starts.
	ApplyRuntimePreparation(ctx context.Context, transfer uuid.UUID, payload proto.RuntimePreparePayload, data []byte) error
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

// ErrEnvironmentUnavailable reports that the owner could not reach the
// Environment's resources and the operation had no effect. The Router ends a
// workspace_write or runtime_prepare that returns it rejected with
// resource_unavailable.
var ErrEnvironmentUnavailable = errors.New("environment unavailable")

var (
	ErrWorkspaceReadUnavailable = errors.New("workspace read unavailable")
	ErrWorkspaceReadInvalid     = errors.New("workspace read invalid")
	ErrWorkspaceReadUncertain   = errors.New("workspace read outcome uncertain")
	// ErrWorkspaceNotDirectory reports that a directory request's own path is
	// missing, a regular file or a symbolic link; the link was not followed.
	ErrWorkspaceNotDirectory = errors.New("workspace path is not a directory")

	ErrWorkspaceWriteBusy      = errors.New("workspace write busy")
	ErrWorkspaceWriteInvalid   = errors.New("workspace write invalid")
	ErrWorkspaceWriteRejected  = errors.New("workspace write rejected")
	ErrWorkspaceWriteUncertain = errors.New("workspace write outcome uncertain")
)

// Known Files.create destination refusals. Both wrap ErrWorkspaceWriteRejected:
// nothing was installed.
var (
	ErrWorkspaceWriteDirectory = fmt.Errorf("%w: destination is a directory", ErrWorkspaceWriteRejected)
	ErrWorkspaceWriteUnsafe    = fmt.Errorf("%w: destination exists or its path is not a plain directory chain", ErrWorkspaceWriteRejected)
)

// validateExecutionEnvironment checks the request's structure;
// proto.ValidateSelection checks what the Harness supports.
func validateExecutionEnvironment(req proto.PromptRequestPayload) error {
	if (req.LocalEnvironment != nil) == req.DisableExecutionEnvironment {
		return errors.New("execution requires exactly one of local_environment and disable_execution_environment")
	}
	if req.MCPHTTPServers == nil {
		return nil
	}
	for _, server := range *req.MCPHTTPServers {
		if err := server.ValidateConnectionOrigin(req); err != nil {
			return err
		}
		if endpoint, err := url.Parse(server.ServerURL); server.BearerToken != nil && (err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "") {
			return errors.New("authenticated HTTP MCP requires HTTPS")
		}
	}
	return nil
}
