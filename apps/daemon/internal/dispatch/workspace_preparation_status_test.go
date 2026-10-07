package dispatch

import (
	"context"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

type workspaceStatusSender chan proto.Envelope

func (s workspaceStatusSender) Send(_ context.Context, envelope proto.Envelope) error {
	s <- envelope
	return nil
}

func TestReadPreparationRetryCannotPublishStaleStatus(t *testing.T) {
	sender := make(workspaceStatusSender, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	r := &Router{sender: sender, shutdownCh: make(chan struct{})}
	p := &preparationState{workspaceReadOnly: true, owns: true, ctx: ctx, cancel: cancel, timer: timer,
		status: proto.PreparationStatusPayload{Handle: "reader", Revision: 2, State: "ready"}}
	// A prepare retry captures this snapshot before the release settles.
	snapshot := p.status
	r.releasePreparation(p, "released", "", true)
	r.shutdownWG.Wait()
	r.publishPreparation(p, snapshot)
	r.shutdownWG.Wait()
	var released proto.PreparationStatusPayload
	if p.owns || len(sender) != 1 || (<-sender).DecodePayload(&released) != nil || released.State != "released" {
		t.Fatal("stale ready snapshot escaped or settled release was not published")
	}
}
