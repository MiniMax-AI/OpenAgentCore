package sandboxlink

import (
	"context"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

func testServer(lost *[]sandboxwire.ID) *server {
	return &server{cfg: ServeConfig{OnAttachmentLost: func(id sandboxwire.ID) { *lost = append(*lost, id) }},
		attachments: map[sandboxwire.ID]*served{}, closedIDs: map[sandboxwire.ID]time.Time{}}
}

// An AttachmentClosed that overtakes a Bind in flight refuses that Bind.
func TestCloseBeforeBind(t *testing.T) {
	var lost []sandboxwire.ID
	s := testServer(&lost)
	id := sandboxwire.NewID()
	s.closed(AttachmentClosed{AttachmentID: id, Reason: CloseRevoked})
	if a, _ := s.track(context.Background(), id); a != nil {
		t.Fatal("a Bind after AttachmentClosed was tracked")
	}
}

// A stream of a closed attachment ending never touches a later attachment of
// the same ID.
func TestReleaseOfClosedAttachment(t *testing.T) {
	var lost []sandboxwire.ID
	s := testServer(&lost)
	id := sandboxwire.NewID()
	old, oldSeq := s.track(context.Background(), id)
	s.closed(AttachmentClosed{AttachmentID: id, Reason: CloseRequested})
	s.closedIDs[id] = time.Now() // let the tombstone lapse
	current, seq := s.track(context.Background(), id)
	if seq <= oldSeq {
		t.Fatalf("bind sequence %d after %d; want it to increase", seq, oldSeq)
	}
	s.release(old)
	if current.streams != 1 || len(lost) != 0 {
		t.Fatalf("after the old stream ended: %d streams, lost %v; want 1 stream and none lost", current.streams, lost)
	}
	s.release(current)
	if len(lost) != 1 {
		t.Fatalf("lost %v, want the current attachment lost", lost)
	}
}

// OnAttachmentClosed runs after the closed attachment's handler context has
// ended, so a service can refuse the handler's late requests by it.
func TestCloseEndsHandlersFirst(t *testing.T) {
	s := testServer(new([]sandboxwire.ID))
	id := sandboxwire.NewID()
	a, _ := s.track(context.Background(), id)
	ended := false
	s.cfg.OnAttachmentClosed = func(sandboxwire.ID, CloseReason) { ended = a.ctx.Err() != nil }
	s.closed(AttachmentClosed{AttachmentID: id, Reason: CloseRevoked})
	if !ended {
		t.Fatal("OnAttachmentClosed ran while the handler context was live")
	}
}
