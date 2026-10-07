package dispatch

import (
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// LocalEnvironments resolves binding as the owner of the Session it binds, as
// the daemon composes it.
func LocalEnvironments(binding *localworkspace.Binding) func(proto.AssignmentRef, proto.AssignmentBindPayload) Environment {
	return func(ref proto.AssignmentRef, bind proto.AssignmentBindPayload) Environment {
		if !binding.Matches(bind.EnvironmentID, ref.SessionID) {
			return nil
		}
		return binding
	}
}

func (r *Router) PreparationOwnershipForTest(handle string) (bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.preparations[handle]
	return p != nil && p.owns, p != nil && p.busy
}

func (r *Router) SteeringClosedForTest(runID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.sessions[runID]
	return state != nil && state.steeringClosed
}

// RunStartedForTest reports whether runID's Turn accepts operations.
func (r *Router) RunStartedForTest(runID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.sessions[runID]
	return state != nil && state.session != nil
}
