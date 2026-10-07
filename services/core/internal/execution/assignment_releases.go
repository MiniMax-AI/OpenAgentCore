package execution

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// runAssignmentReleases delivers each released assignment to its connected
// Runtime until the Runtime acknowledges it, so a Runtime that reconnects
// receives the releases it missed. Releases run one per Session, bounded like
// executions. A failed release backs off, and the release due longest goes
// first, so failing releases cannot hold the slots that others need.
func (w *Worker) runAssignmentReleases(ctx context.Context) error {
	type outcome struct {
		ref          proto.AssignmentRef
		acknowledged bool
	}
	active := make(map[string]bool)
	retries := releaseRetries{}
	done := make(chan outcome, w.executionConcurrency())
	var running sync.WaitGroup
	ctx, stop := context.WithCancel(ctx)
	defer func() { stop(); running.Wait() }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-done:
			delete(active, result.ref.SessionID)
			retries.record(result.ref, result.acknowledged, time.Now())
			continue
		case <-ticker.C:
		}
		releases, err := w.dispatcher.sessionExecution.ListAssignmentReleases(ctx, w.dispatcher.Registry.Devices())
		if err != nil {
			return err
		}
		retries.keep(releases)
		for _, release := range retries.due(releases, time.Now()) {
			ref := release.Assignment
			if active[ref.SessionID] || len(active) >= w.executionConcurrency() {
				continue
			}
			active[ref.SessionID] = true
			running.Add(1)
			go func() {
				defer running.Done()
				done <- outcome{ref: ref, acknowledged: w.releaseAssignment(ctx, release)}
			}()
		}
	}
}

// releaseRetries holds each failed release's next attempt. The delay doubles
// from a second up to a minute.
type releaseRetries map[proto.AssignmentRef]releaseRetry

type releaseRetry struct {
	at    time.Time
	delay time.Duration
}

// due returns the releases whose backoff has passed, the one due longest
// first; a release never attempted is due from the start.
func (r releaseRetries) due(releases []sessions.AssignmentRelease, now time.Time) []sessions.AssignmentRelease {
	due := slices.DeleteFunc(slices.Clone(releases), func(release sessions.AssignmentRelease) bool {
		return now.Before(r[release.Assignment].at)
	})
	slices.SortStableFunc(due, func(a, b sessions.AssignmentRelease) int {
		return r[a.Assignment].at.Compare(r[b.Assignment].at)
	})
	return due
}

func (r releaseRetries) record(ref proto.AssignmentRef, acknowledged bool, now time.Time) {
	if acknowledged {
		delete(r, ref)
		return
	}
	delay := min(max(2*r[ref].delay, time.Second), time.Minute)
	r[ref] = releaseRetry{at: now.Add(delay), delay: delay}
}

// keep forgets releases that are no longer pending for a connected Runtime.
func (r releaseRetries) keep(releases []sessions.AssignmentRelease) {
	pending := make(map[proto.AssignmentRef]bool, len(releases))
	for _, release := range releases {
		pending[release.Assignment] = true
	}
	for ref := range r {
		if !pending[ref] {
			delete(r, ref)
		}
	}
}

// releaseAssignment sends one release and records its acknowledgement. Home
// removal is requested only from a Runtime that declares it.
func (w *Worker) releaseAssignment(ctx context.Context, release sessions.AssignmentRelease) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	peer, err := w.dispatcher.authorizedPeer(ctx, release.RuntimeID)
	if err != nil {
		return false
	}
	supported, known := peer.RemovesHomes()
	if !known {
		return false
	}
	removeHome := release.RemoveHome && supported
	want := proto.AssignmentReleased
	if removeHome {
		want = proto.AssignmentHomeRemoved
	}
	status, err := peer.Release(ctx, release.Assignment, removeHome)
	if err != nil || status.State != want {
		log.Warn(ctx, "Runtime assignment release unconfirmed", "session_id", release.Assignment.SessionID, "runtime_id", release.RuntimeID, "error_code", status.ErrorCode)
		return false
	}
	if err := w.dispatcher.sessionExecution.AcknowledgeAssignmentRelease(ctx, release.Assignment); err != nil {
		log.Warn(ctx, "Runtime assignment release not recorded", "session_id", release.Assignment.SessionID, "runtime_id", release.RuntimeID)
		return false
	}
	return true
}
