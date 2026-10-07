package runtimegateway

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func workspaceReadRequest() proto.WorkspaceReadPayload {
	return proto.WorkspaceReadPayload{Handle: "prepared", EnvironmentID: "environment", Path: "directory", MaxEntries: 2}
}

func TestWorkspaceReadValidatesRejections(t *testing.T) {
	for _, test := range []struct {
		result   proto.WorkspaceReadResultPayload
		accepted bool
	}{
		{proto.WorkspaceReadResultPayload{Outcome: "rejected", ErrorCode: proto.WorkspaceReadNotDirectory}, true},
		{proto.WorkspaceReadResultPayload{Outcome: "rejected", ErrorCode: "not_found"}, true},
		{proto.WorkspaceReadResultPayload{Outcome: "rejected", ErrorCode: proto.AssignmentStale}, true},
		{proto.WorkspaceReadResultPayload{Outcome: "rejected", ErrorCode: proto.AssignmentConflict}, true},
		{proto.WorkspaceReadResultPayload{Outcome: "rejected", ErrorCode: "native-private-secret"}, false},
		{proto.WorkspaceReadResultPayload{Outcome: "rejected", ErrorCode: proto.WorkspaceReadNotDirectory, CloseAcknowledged: true}, false},
		{proto.WorkspaceReadResultPayload{Outcome: "rejected", ErrorCode: proto.WorkspaceReadNotDirectory, Directory: &proto.WorkspaceDirectoryResult{Entries: []proto.WorkspaceDirectoryEntry{}}}, false},
		{proto.WorkspaceReadResultPayload{Outcome: "unknown", ErrorCode: proto.WorkspaceReadNotDirectory}, false},
		{proto.WorkspaceReadResultPayload{Outcome: "completed", CloseAcknowledged: true, ErrorCode: proto.WorkspaceReadNotDirectory, Directory: &proto.WorkspaceDirectoryResult{Entries: []proto.WorkspaceDirectoryEntry{}}}, false},
	} {
		s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
		done := make(chan error, 1)
		go func() {
			_, err := s.ListWorkspaceDirectory(t.Context(), testAssignment, workspaceReadRequest())
			done <- err
		}()
		request := <-s.sendCh
		reply, _ := request.Reply(proto.TypeWorkspaceReadResult, test.result)
		s.dispatch(reply)
		if err := <-done; (err == nil) != test.accepted {
			t.Fatal("directory result validation changed", test.result, err)
		}
		s.Close("test")
	}
}

func TestWorkspaceReadObserverCancellationDoesNotSendCancelOrRetry(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
	defer s.Close("test")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := s.ListWorkspaceDirectory(ctx, testAssignment, workspaceReadRequest()); done <- err }()
	request := <-s.sendCh
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	reply, _ := request.Reply(proto.TypeWorkspaceReadResult, proto.WorkspaceReadResultPayload{Outcome: "unknown", ErrorCode: "read_unconfirmed"})
	s.dispatch(reply)
	select {
	case extra := <-s.sendCh:
		t.Fatal("observer caused another control", extra.Type)
	default:
	}
	s.workspaceReadMu.Lock()
	count := len(s.workspaceReads)
	s.workspaceReadMu.Unlock()
	if count != 0 {
		t.Fatal("observer subscription leaked")
	}
}

func TestWorkspaceReadCapacityAndConnectionLoss(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
	done := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			_, err := s.ListWorkspaceDirectory(t.Context(), testAssignment, workspaceReadRequest())
			done <- err
		}()
		select {
		case <-s.sendCh:
		case <-time.After(time.Second):
			t.Fatal("read not sent")
		}
	}
	if _, err := s.ListWorkspaceDirectory(t.Context(), testAssignment, workspaceReadRequest()); err == nil {
		t.Fatal("capacity bypassed")
	}
	s.Close("connection lost")
	for i := 0; i < 4; i++ {
		if err := <-done; !errors.Is(err, ErrSessionClosed) {
			t.Fatal(err)
		}
	}
}

func TestWorkspaceReadRejectsOversizedRequestsBeforeQueueing(t *testing.T) {
	for _, field := range []string{"path", "handle", "environment", "escaped"} {
		t.Run(field, func(t *testing.T) {
			s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
			defer s.Close("test")
			request := workspaceReadRequest()
			oversized := strings.Repeat("x", int(ReadLimit))
			switch field {
			case "path":
				request.Path = oversized
			case "handle":
				request.Handle = oversized
			case "environment":
				request.EnvironmentID = oversized
			case "escaped":
				request.Path = strings.Repeat("\x00", proto.WorkspaceReadMaxRequestBytes/2)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := s.ListWorkspaceDirectory(ctx, testAssignment, request); err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("oversized request not rejected before send", err)
			}
			select {
			case <-s.sendCh:
				t.Fatal("oversized request queued")
			default:
			}
		})
	}
}
