package localworkspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Binding freezes operator-owned identity and paths for one Runtime lifetime.
type Binding struct {
	environment    string
	networkAccess  string
	allowedDomains []string
	session        string
	workspace      string
	writer         *fileWriter
	capabilityMu   sync.Mutex
	capabilityRoot string
}

// NewWithCapabilityDirectory freezes paths selected by the Runtime operator.
func NewWithCapabilityDirectory(environment, session, workspace, directory string) (*Binding, error) {
	return newNativeBinding(environment, session, workspace, directory)
}

func Load() (*Binding, error) {
	values := []string{os.Getenv("OAC_RUNTIME_ENVIRONMENT_ID"), os.Getenv("OAC_RUNTIME_SESSION_ID"), os.Getenv("OAC_RUNTIME_WORKSPACE")}
	policy, err := RuntimeNetworkPolicy()
	if err != nil {
		return nil, err
	}
	network := policy.Access
	capabilityDirectory := os.Getenv("OAC_RUNTIME_CAPABILITY_DIRECTORY")
	if strings.Join(values, "") == "" && network == "" && capabilityDirectory == "" {
		return nil, nil
	}
	if capabilityDirectory == "" {
		capabilityDirectory = CapabilityDirectory
	}
	b, err := NewWithCapabilityDirectory(values[0], values[1], values[2], capabilityDirectory)
	if err != nil {
		return nil, err
	}
	b.networkAccess = network
	b.allowedDomains = policy.Hosts()

	return b, nil
}

// Configure checks the request against the bound Environment and workspace.
func (b *Binding) Configure(r proto.PromptRequestPayload) error {
	local := r.LocalEnvironment
	switch {
	case local == nil || local.ID != b.environment || r.DisableExecutionEnvironment:
		return errors.New("request does not match the dedicated local Environment")
	case r.WorkspaceReadOnly:
		return nil
	case local.WorkspaceDirectory != "/workspace" && local.WorkspaceDirectory != b.workspace:
		return errors.New("request does not match the local workspace selection")
	case local.CapabilitySources == nil || agentcapabilities.ValidateInput(*local.CapabilitySources) != nil:
		return agentcapabilities.ErrInvalid
	}
	return nil
}

// Resolve is the Session's Environment owner: b for the one Session it is
// bound to, and none for any other.
func (b *Binding) Resolve(ref proto.AssignmentRef, bind proto.AssignmentBindPayload) dispatch.Environment {
	if !b.Matches(bind.EnvironmentID, ref.SessionID) {
		return nil
	}
	return b
}

// Support is what the guest serves: its bound Session's local Environment,
// or, when b is nil, environment none.
func (b *Binding) Support() agent.EnvironmentSupport {
	return agent.EnvironmentSupport{Local: b != nil, None: b == nil}
}

func (b *Binding) Matches(environment, session string) bool {
	return b != nil && b.environment == environment && b.session == session
}

// Close keeps the workspace, which outlives each assignment of its Session.
func (b *Binding) Close(context.Context) error { return nil }
