package dispatch

func (r *Router) closePreparationResource(p *preparationState) {
	// Only the operation that owns busy calls this; other paths cancel its owner.
	r.mu.Lock()
	p.busy, p.owns = false, false
	p.status.Revision++
	status := p.status
	r.mu.Unlock()
	r.publishPreparation(p, status)
}

func (r *Router) closePendingPreparationsLocked() []*preparationState {
	var closeNow []*preparationState
	for _, p := range r.preparations {
		p.timer.Stop()
		if p.executor != nil {
			continue
		}
		if !p.owns {
			continue
		}
		p.cancel()
		switch p.status.State {
		case "preparing", "ready", "starting":
			p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "connection_closed", p.status.Revision+1
		}
		if !p.busy {
			p.busy = true
			r.shutdownWG.Add(1)
			closeNow = append(closeNow, p)
		}
	}
	return closeNow
}
