//go:build linux

package worldfs

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
)

// cleanup is a Release or ReleaseDir of a handle the kernel closed.
type cleanup struct {
	handle sandboxfs.HandleID
	dir    bool
}

// wake runs the drainer without blocking.
func (f *frontend) wake() {
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

// drain sends what the kernel released: the queued Forget references and the Release and ReleaseDir requests a failed stream never sent. It runs until Stop.
func (f *frontend) drain() {
	defer close(f.drained)
	for {
		select {
		case <-f.drainCtx.Done():
			return
		case <-f.kick:
		}
		f.sendForgets()
		f.mu.Lock()
		rs := f.releases
		f.releases = nil
		f.mu.Unlock()
		for _, c := range rs {
			f.release(f.drainCtx, c)
		}
	}
}

// sendForgets sends the queued references in batches. A batch the stream never sent goes back to the queue; one that may have been applied is not sent again.
func (f *frontend) sendForgets() {
	f.mu.Lock()
	batch := f.forgets
	f.forgets = map[sandboxfs.NodeRef]uint64{}
	f.mu.Unlock()
	entries := make([]sandboxfs.ForgetEntry, 0, len(batch))
	for ref, n := range batch {
		entries = append(entries, sandboxfs.ForgetEntry{Node: ref, Count: n})
	}
	for len(entries) > 0 {
		n := min(len(entries), forgetBatch)
		if _, err := call(f, f.drainCtx, (*sandboxfs.Client).Forget, &sandboxfs.ForgetRequest{Entries: entries[:n]}); err != nil && unsent(err) && !f.dead.Load() {
			f.mu.Lock()
			for _, e := range entries {
				f.forgets[e.Node] += e.Count
			}
			f.mu.Unlock()
			return
		}
		entries = entries[n:]
	}
}

// release sends a Release or ReleaseDir. One the stream never sent is queued for the drainer, which runs after the next redial; one that may have reached the service is never sent again.
func (f *frontend) release(ctx context.Context, c cleanup) {
	var err error
	if c.dir {
		_, err = call(f, ctx, (*sandboxfs.Client).ReleaseDir, &sandboxfs.ReleaseDirRequest{Handle: c.handle})
	} else {
		_, err = call(f, ctx, (*sandboxfs.Client).Release, &sandboxfs.ReleaseRequest{Handle: c.handle})
	}
	if err != nil && unsent(err) && !f.dead.Load() && !f.closed.Load() {
		f.mu.Lock()
		f.releases = append(f.releases, c)
		f.mu.Unlock()
	}
}
