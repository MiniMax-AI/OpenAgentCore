package dispatch

func (r *Router) closePendingPreparationsLocked() {
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
		case "preparing", "ready":
			p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "connection_closed", p.status.Revision+1
		}
		// A busy preparation drops ownership when readiness returns.
		if !p.busy {
			p.owns = false
		}
	}
}
