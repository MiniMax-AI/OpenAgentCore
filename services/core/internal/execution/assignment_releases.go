package execution

import (
	"context"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// runAssignmentReleases delivers each released assignment to its connected
// Runtime until the Runtime acknowledges it, so a Runtime that reconnects
// receives the releases it missed. Releases run one per Session, bounded like
// executions.
func (w *Worker) runAssignmentReleases(ctx context.Context) error {
	active := make(map[string]bool)
	done := make(chan string, w.executionConcurrency())
	var running sync.WaitGroup
	ctx, stop := context.WithCancel(ctx)
	defer func() { stop(); running.Wait() }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case session := <-done:
			delete(active, session)
			continue
		case <-ticker.C:
		}
		releases, err := w.dispatcher.sessionExecution.ListAssignmentReleases(ctx, w.dispatcher.Registry.Devices())
		if err != nil {
			return err
		}
		for _, release := range releases {
			session := release.Assignment.SessionID
			if active[session] || len(active) >= w.executionConcurrency() {
				continue
			}
			active[session] = true
			running.Add(1)
			go func() {
				defer running.Done()
				w.releaseAssignment(ctx, release)
				done <- session
			}()
		}
	}
}

// releaseAssignment sends one release and records its acknowledgement. Home
// removal is requested only from a Runtime that declares it.
func (w *Worker) releaseAssignment(ctx context.Context, release sessions.AssignmentRelease) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	peer, err := w.dispatcher.authorizedPeer(ctx, release.RuntimeID)
	if err != nil {
		return
	}
	supported, known := peer.RemovesHomes()
	if !known {
		return
	}
	removeHome := release.RemoveHome && supported
	want := proto.AssignmentReleased
	if removeHome {
		want = proto.AssignmentHomeRemoved
	}
	status, err := peer.Release(ctx, release.Assignment, removeHome)
	if err != nil || status.State != want {
		log.Warn(ctx, "Runtime assignment release unconfirmed", "session_id", release.Assignment.SessionID, "runtime_id", release.RuntimeID, "error_code", status.ErrorCode)
		return
	}
	if err := w.dispatcher.sessionExecution.AcknowledgeAssignmentRelease(ctx, release.Assignment); err != nil {
		log.Warn(ctx, "Runtime assignment release not recorded", "session_id", release.Assignment.SessionID, "runtime_id", release.RuntimeID)
	}
}
