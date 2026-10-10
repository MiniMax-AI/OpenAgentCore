package workspaces

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
)

// Storage reads configurations and ownership; it grants no execution authority.
type Storage interface {
	ActiveConfiguration(context.Context) (workspacefs.Configuration, error)
	Get(context.Context, string, string) (Record, error)
	// DeletionCandidates includes explicitly deleted Sessions even when no
	// compute allocation was ever made, and resumes committed deletion intent.
	// Unreleased compute ownership excludes a workspace from this list.
	DeletionCandidates(context.Context, string) ([]Record, error)
}

// ExecutionStorage writes only through the existing execution lease. Session
// locking serializes admission and ready publication against public deletion.
type ExecutionStorage interface {
	// SelectConfiguration commits the selection with administrator audit on
	// the execution lease, retaining immutable previous configurations.
	SelectConfiguration(context.Context, workspacefs.Configuration) error
	// Bind commits an identity and the selected configuration before external
	// creation. Retries return that same binding across configuration changes.
	Bind(context.Context, string, string) (Record, error)
	MarkReady(context.Context, workspacefs.Reference, workspacefs.Attachment) error
	// BeginDelete requires an explicitly deleted Session and released compute.
	BeginDelete(context.Context, workspacefs.Reference) error
	MarkDeleted(context.Context, workspacefs.Reference) error
}
