// Package agent defines the Runtime's native Harness adapter contract.
// Start with this file when adding a Harness; the onboarding guide is at
// contracts/agents-api/harness-onboarding.md.
//
// Required lifecycle: ExecutorFactory prepares a fixed Session-owned Executor;
// each StartTurn returns a fresh Turn with its own output and settlement.
// Turn includes the public text DurableSteerer requirement. Keep extension
// interfaces separate, but implement each explicitly: unsupported operations
// return ErrUnsupportedOperation before any native effects. Interface presence
// does not advertise support; the capability declaration controls admission. MCP,
// images, structured output and Subagent observations use protocol messages
// rather than additional Go interfaces; qualify and advertise them separately.
//
// Registration: each adapter exports one Declaration. The Runtime discovers the
// static declaration list and installs each resulting Runtime through Register.
// Availability and factory selection belong to the adapter. RegisterKind resets
// the factories, so Register installs it first. RegisterExecutor derives the
// Preparation capability; the adapter declares WorkspaceReadPreparation.
//
// Runtime registration and Core service qualification remain separate. A public
// Harness also needs a profile in services/core/internal/engine; advertising
// a capability cannot authorize it. Requests, events and capability descriptors
// use the existing internal/agentdaemon/proto types. An Environment execution
// request carries the Runtime's bound workspace directory in
// LocalEnvironment.WorkspaceRoot; the native Harness runs there.
package agent

import (
	"context"
	"io"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

// Declaration is the complete startup contract for a Harness implementation.
// Discover returns nil when the adapter is not configured. An unavailable
// configured adapter returns a Runtime with Available=false and no factories.
// Discovery owns runtime-specific configuration, readiness and feature gates.
type Declaration struct {
	Info          proto.SupportedAgentKind
	Configuration harnessconfig.Configuration
	Discover      func(context.Context, DiscoveryOptions, proto.SupportedAgentKind) *Runtime
}

// DiscoveryOptions provides process context without naming an implementation.
type DiscoveryOptions struct {
	Profile string
	// StateRoot holds private history retained with the Environment filesystem.
	StateRoot string
	// RuntimeRoot holds instance-private configuration and process files.
	RuntimeRoot    string
	Stdout, Stderr io.Writer
}

// Runtime binds one discovered descriptor to its native factories.
type Runtime struct {
	Info     proto.SupportedAgentKind
	Executor ExecutorFactory
}

// Register installs a discovered Runtime with its declaration's configuration.
func (r *Registry) Register(declaration Declaration, runtime Runtime) {
	if runtime.Info.Kind != declaration.Info.Kind {
		panic("agent.Registry.Register: discovery kind differs from declaration")
	}
	if !runtime.Info.Available && runtime.Executor != nil {
		panic("agent.Registry.Register: unavailable runtime has factories")
	}
	r.RegisterKind(runtime.Info, declaration.Configuration)
	if runtime.Executor != nil {
		r.RegisterExecutor(runtime.Info.Kind, runtime.Executor)
	}
}

// Model configuration has one shared contract, authored in
// internal/harnessconfig/harness.go. RegisterKind requires that declaration;
// RegisterExecutor inherits it. Every registered entry
// validates model, provider and native parameters before calling native code.
// The declaration belongs to the adapter and is also consumed by Core. Keep
// adapter field rules and rendering private. That shared contract owns frozen
// configuration and native application obligations. This file owns execution
// lifecycle only. Native image/tool/operation support is qualified through proto
// capabilities and the Core engine profile, not model configuration declarations.
//
// Preparation failure retains unconfirmed native cleanup in a non-nil Executor
// under the factory ownership contract below.

// Required execution lifecycle.

// ExecutorFactory prepares without model input. A failed factory retains any
// unconfirmed cleanup in its non-nil Executor.
type ExecutorFactory func(context.Context, proto.PromptRequestPayload) (Executor, error)

// Executor retains a fixed native configuration across independently owned Turns.
type Executor interface {
	// A nil Turn guarantees no input was submitted or output retained and leaves
	// out with the caller. A non-nil Turn owns out, including on error; input
	// may have been submitted and must never be replayed automatically.
	StartTurn(context.Context, string, proto.MessageInput, chan<- proto.Envelope) (Turn, error)
	// Success confirms all native work and owned output streams have stopped.
	// It does not establish an otherwise unknown Turn result. An error retains
	// resource ownership; another caller may await or retry Close.
	Close(context.Context) error
}

// Turn owns one output stream and never retargets cancellation to a successor.
type Turn interface {
	Session
	DurableSteerer
	// Success confirms closed output and settled native input, function and
	// child-work obligations. Errors cannot prove cancellation.
	AwaitSettlement(context.Context) (TurnSettlement, error)
}

// TurnSettlement describes native readiness after this Turn has fully drained.
// A false result must carry a reason; it requires confirmed Executor.Close.
type TurnSettlement struct {
	Reusable bool
	Reason   string
}

// Session is the cancellation and outcome surface of a Turn. Every owner
// exposes observed state.
// For Executor-owned Turns, AwaitSettlement and Executor.Close define settlement
// and resource retirement; Cancel alone does not transfer resource ownership.
type Session interface {
	// Cancel signals the session to abort. Idempotent. Actual teardown
	// happens asynchronously and is signalled via the out channel close.
	Cancel(ctx context.Context) error
	// CancellationOutcome snapshots observed native identity, usage and output.
	// It remains readable after Cancel; missing evidence stays unset. An empty
	// result means no observed evidence, not unsupported cancellation or success.
	// Reading it does not wait for or establish native settlement.
	CancellationOutcome() proto.DonePayload
}

// Turn extension contracts. Every public Harness implements each interface;
// unsupported operations return ErrUnsupportedOperation with a fixed safe reason.

// DurableSteerer reports one complete write synchronously, then waits for the native receipt.
// It is independent of Steerer and is mandatory on every public Turn.
// Required input receipts cannot be implemented by returning Unsupported.
type DurableSteerer interface {
	SteerWithReceipt(context.Context, proto.PromptSteerPayload, func()) error
}

// Steerer delivers non-durable input when qualified; otherwise it explicitly
// returns ErrUnsupportedOperation without submitting input.
type Steerer interface {
	Steer(context.Context, proto.PromptSteerPayload) error
}

// FunctionResultSubmitter delivers a result for an outstanding native call.
// A successful return requires its native application receipt.
type FunctionResultSubmitter interface {
	SubmitFunctionResult(context.Context, proto.FunctionResultPayload) error
}

// Workspace extensions, implemented by each owner explicitly. The common
// Runtime may supply an authorized workspace owner independently of the adapter.
// A resource without native access returns the corresponding Unsupported error;
// this does not disable capabilities provided by the common workspace owner.

// WorkspaceReader returns success only after acknowledged native close on an existing owner.
type WorkspaceReader interface {
	ReadWorkspaceFile(context.Context, string, int) (WorkspaceReadResult, error)
}

// WorkspaceDirectoryLister reads one directory through an existing workspace owner.
// An empty directory selects the root; other paths contain only relative components.
// Entry names are single components. Kind is file, directory, symlink or other;
// SizeBytes is present and nonnegative only for regular files. Results have no
// prescribed order, and Truncated must not be presented as a complete inventory.
// maxEntries is positive; adapters may reject limits above their private bound.
// Successful return requires settled directory/metadata access and handle cleanup.
// Implementations retain workspace authorization and isolation and use the existing
// WorkspaceRead errors for unsupported, unavailable, busy, invalid or uncertain reads.
// This interface does not establish public Files pagination or feature admission.
type WorkspaceDirectoryLister interface {
	ListWorkspaceDirectory(context.Context, string, int) (WorkspaceDirectoryResult, error)
}

// WorkspaceWriter confirms a native commit on an already authorized prepared owner.
type WorkspaceWriter interface {
	WriteWorkspaceFile(context.Context, string, []byte) (WorkspaceWriteResult, error)
}

// Kind registration.

// RegisterKind installs the heartbeat descriptor and model configuration for an
// agent_kind. Callers may set Available=false when an adapter exists but its
// underlying CLI is not usable.
func (r *Registry) RegisterKind(info proto.SupportedAgentKind, configuration harnessconfig.Configuration) {
	kind := info.Kind
	if kind == "" {
		panic("agent.Registry.Register: empty kind")
	}
	if err := configuration.ValidateDeclaration(); err != nil {
		panic(err)
	}
	if err := info.ValidateDeclaration(); err != nil {
		panic(err)
	}
	configuration = configuration.Clone()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.configurations[kind] = configuration
	delete(r.executors, kind)
	info.Capabilities.Preparation = proto.CapabilityUnsupported
	r.kinds[kind] = info
}

// RegisterExecutor installs the shared lifecycle after RegisterKind and derives
// the Preparation capability. It does not enable other public operations.
func (r *Registry) RegisterExecutor(kind string, factory ExecutorFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, exists := r.kinds[kind]
	if !exists || factory == nil {
		panic("agent.Registry.RegisterExecutor: registered kind and factory required")
	}
	configuration, declared := r.configurations[kind]
	if !declared {
		panic("agent.Registry.RegisterExecutor: configuration required")
	}
	r.executors[kind] = func(ctx context.Context, req proto.PromptRequestPayload) (Executor, error) {
		if _, err := configuration.Prepare(req.AgentOptions); err != nil {
			return nil, err
		}
		return factory(ctx, req)
	}
	info.Capabilities.Preparation = proto.CapabilitySupported
	r.kinds[kind] = info
}
