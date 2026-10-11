package workspaces

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
)

// Storage reads configurations and ownership; it grants no execution authority.
// Records and configurations carry their decrypted credentials; Selection is public.
type Storage interface {
	ActiveConfiguration(context.Context) (workspacefs.ConfigurationRecord, error)
	// Configuration returns a retained configuration by ID, or ErrNotFound.
	Configuration(context.Context, string) (workspacefs.ConfigurationRecord, error)
	Selection(context.Context) (Selection, error)
	Get(context.Context, string, string) (Record, error)
	// DeletionCandidates includes explicitly deleted Sessions even when no
	// compute allocation was ever made, and resumes committed deletion intent.
	// Unreleased compute ownership excludes a workspace from this list.
	DeletionCandidates(context.Context, string) ([]Record, error)
}

// ExecutionStorage writes only through the existing execution lease. Session
// locking serializes admission and ready publication against public deletion.
type ExecutionStorage interface {
	// SelectConfiguration commits the selection and the tenant capacity policy
	// together with administrator audit on the execution lease, retaining
	// immutable previous configurations. Reusing an ID requires the same
	// adapter, parameters and credential.
	SelectConfiguration(context.Context, workspacefs.ConfigurationRecord, uint32) error
	// Bind returns an existing binding unchanged, across configuration and
	// policy changes. Otherwise it admits a new identity with the expected
	// configuration ID and capacity: ErrConflict if another configuration is
	// now selected; under a tenant capacity policy, ErrUnsupported for an
	// unbounded request and ErrCapacityExhausted when the tenant's workspaces
	// that are not yet deleted would exceed it or include an unbounded one.
	Bind(ctx context.Context, tenant, environment, configuration string, capacityMiB uint32) (Record, error)
	MarkReady(context.Context, workspacefs.Reference, workspacefs.Attachment) error
	// BeginDelete requires an explicitly deleted Session and released compute.
	BeginDelete(context.Context, workspacefs.Reference) error
	MarkDeleted(context.Context, workspacefs.Reference) error
}
