package dispatch

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
