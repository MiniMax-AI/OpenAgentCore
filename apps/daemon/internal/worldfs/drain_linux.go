//go:build linux

package worldfs

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
)

// cleanup is a Release or ReleaseDir of a handle the kernel closed, or of a handle ID whose acquisition may have taken effect without a response.
type cleanup struct {
	handle sandboxfs.HandleID
	dir    bool
}

// wake runs the drainer without blocking. Whoever queues work calls it after queuing, so the drainer, which takes the queues after each wakeup, always sees the work.
func (f *frontend) wake() {
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

// drain sends what the kernel released: queued Forget references and the Release and ReleaseDir requests that could not be sent. After a pass that leaves something queued it waits, doubling the wait from retryMin to retryMax, unless a redial gives it a new stream first. It runs until Stop.
func (f *frontend) drain() {
	defer close(f.drained)
	var retry <-chan time.Time
	var backoff time.Duration
	var failedOn uint64 // the stream generation the last pass that left work queued failed on
	for {
		select {
		case <-f.drainCtx.Done():
			return
		case <-retry:
		case <-f.kick:
			if retry != nil && f.gen.Load() == failedOn {
				continue // no new stream: newly queued work waits for the retry with the rest
			}
		}
		if f.flush() {
			retry, backoff = nil, 0
			continue
		}
		// f.gen now counts every redial up to the failure, the pass's own included, so only a stream opened after the failure ends the wait early.
		failedOn, backoff = f.gen.Load(), min(max(2*backoff, retryMin), retryMax)
		retry = time.After(backoff)
	}
}

// flush sends everything queued and reports whether nothing was left to retry. It stops at the first request it must retry, which goes back to the queue with everything after it.
func (f *frontend) flush() bool {
	if !f.sendForgets() {
		return false
	}
	f.mu.Lock()
	rs := f.releases
	f.releases = nil
	f.mu.Unlock()
	for i, c := range rs {
		if !f.sendRelease(f.drainCtx, c) {
			f.mu.Lock()
			f.releases = append(rs[i:], f.releases...)
			f.mu.Unlock()
			return false
		}
	}
	return true
}

// sendForgets sends the queued references in batches. A batch that certainly did nothing goes back to the queue with the rest; one that may have been applied is not sent again.
func (f *frontend) sendForgets() bool {
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
		_, err := call(f, f.drainCtx, (*sandboxfs.Client).Forget, &sandboxfs.ForgetRequest{Entries: entries[:n]})
		if err != nil && retryable(err) && !f.dead.Load() {
			f.mu.Lock()
			for _, e := range entries {
				f.forgets[e.Node] += e.Count
			}
			f.mu.Unlock()
			return false
		}
		entries = entries[n:]
	}
	return true
}

// settle releases the handle ID of an acquisition that failed after it may have taken effect. Sent after the acquisition, on its stream or on a successor the service fences behind it, the Release finds whatever the acquisition left. The acquisition itself is never sent again.
func (f *frontend) settle(c cleanup, err error) {
	if !noEffect(err) {
		f.release(c)
	}
}

// release sends a Release or ReleaseDir for a handle the kernel closed, or for an acquisition in doubt. One the service has not answered is queued for the drainer.
func (f *frontend) release(c cleanup) {
	if f.sendRelease(f.ctx, c) {
		return
	}
	if f.seams.queue != nil {
		f.seams.queue()
	}
	f.mu.Lock()
	f.releases = append(f.releases, c)
	f.wake()
	f.mu.Unlock()
}

// sendRelease sends c and reports false when it must be sent again: when it certainly did nothing and may succeed later, or when the stream failed before its answer arrived. The frontend never reuses a handle ID, so a repeat of a Release that ran finds StaleHandle, which settles it as success does.
func (f *frontend) sendRelease(ctx context.Context, c cleanup) bool {
	var err error
	if c.dir {
		_, err = call(f, ctx, (*sandboxfs.Client).ReleaseDir, &sandboxfs.ReleaseDirRequest{Handle: c.handle})
	} else {
		_, err = call(f, ctx, (*sandboxfs.Client).Release, &sandboxfs.ReleaseRequest{Handle: c.handle})
	}
	return err == nil || !retryable(err) && !errors.Is(err, sandboxfs.ErrTransport) || f.dead.Load() || f.closed.Load()
}
