package localworkspace

import (
	"errors"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimebootstrap"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Binding freezes operator-owned identity and paths for one Runtime lifetime.
type Binding struct {
	environment    string
	stateKey       string
	workspace      string
	writer         *fileWriter
	capabilityMu   sync.Mutex
	capabilityRoot string
	stateRoot      string
}

// NewWithCapabilityDirectory freezes paths selected by the Runtime operator.
func NewWithCapabilityDirectory(environment, session, workspace, directory string) (*Binding, error) {
	stateRoot, err := paths.StateDirectory()
	if err != nil {
		return nil, err
	}
	b, err := newNativeBinding(environment, session, workspace, directory)
	if err != nil {
		return nil, err
	}
	b.stateRoot = stateRoot
	return b, nil
}

var current *Binding

// Bind freezes the Runtime's Environment binding for this process. It runs once, before any Harness discovery.
func Bind(environment, session, workspace, capabilityDirectory string) error {
	b, err := NewWithCapabilityDirectory(environment, session, workspace, capabilityDirectory)
	if err != nil {
		return err
	}
	current = b
	return nil
}

// Unbind clears the Binding set by Bind.
func Unbind() { current = nil }

// Current returns the Binding set by Bind, or nil when the Runtime serves no dedicated Environment.
func Current() *Binding { return current }

// Workspace is the directory bound as the Environment's workspace.
func (b *Binding) Workspace() string { return b.workspace }

// Environment is the bound Environment ID.
func (b *Binding) Environment() string { return b.environment }

// Configure validates the reference before supplying the immutable local cwd.
func (b *Binding) Configure(r proto.PromptRequestPayload) (proto.PromptRequestPayload, error) {
	if b == nil && r.LocalEnvironment == nil {
		return r, nil
	}
	if b == nil || r.LocalEnvironment == nil || r.LocalEnvironment.ID != b.environment || r.AgentStateKey != b.stateKey ||
		r.DisableExecutionEnvironment {
		return r, errors.New("request does not match the dedicated local Environment")
	}
	if !r.WorkspaceReadOnly {
		local := *r.LocalEnvironment
		if local.WorkspaceDirectory != runtimebootstrap.PublicWorkspace && local.WorkspaceDirectory != b.workspace {
			return r, errors.New("request does not match the local workspace selection")
		}
		if local.CapabilitySources == nil || proto.ValidateCapabilitySources(*local.CapabilitySources) != nil {
			return r, agentcapabilities.ErrInvalid
		}
		sources := *local.CapabilitySources
		present := len(sources.Skills)+len(sources.Plugins)+len(sources.Directories) > 0
		if present != local.Capabilities {
			return r, agentcapabilities.ErrInvalid
		}
		local.Skills, local.MCP, local.CapabilityRoot = nil, nil, ""
		local.WorkspaceRoot = b.workspace
		r.LocalEnvironment = &local
	}
	return r, nil
}

func (b *Binding) Matches(environment, session string) bool {
	return b != nil && b.environment == environment && b.stateKey == session
}
