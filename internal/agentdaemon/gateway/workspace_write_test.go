package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func TestWorkspaceWriteChunksAndCorrelatesReceipt(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
	defer s.Close("test")
	data := bytes.Repeat([]byte{0, 255, 3}, 400000)
	id := uuid.NewString()
	request := proto.WorkspaceWritePayload{EnvironmentID: uuid.NewString(), SessionID: uuid.NewString(), Path: "file"}
	done := make(chan error, 1)
	go func() {
		result, err := s.WriteWorkspaceFile(t.Context(), id, request, data)
		if err == nil && (result.Outcome != "completed" || result.SizeBytes != len(data)) {
			err = errors.New("missing commit")
		}
		done <- err
	}()
	var received []byte
	for {
		env := <-s.sendCh
		var p proto.WorkspaceWritePayload
		if env.ID != id || env.Type != proto.TypeWorkspaceWrite || len(env.Payload) > proto.WorkspaceWriteMaxFrameBytes || env.DecodePayload(&p) != nil || !proto.ValidWorkspaceWriteRequest(p) {
			t.Fatal("invalid transfer frame")
		}
		result := proto.WorkspaceWriteResultPayload{}
		switch p.Step {
		case "begin":
			digest := sha256.Sum256(data)
			if p.EnvironmentID != request.EnvironmentID || p.SessionID != request.SessionID || p.SizeBytes != len(data) || p.SHA256 != hex.EncodeToString(digest[:]) {
				t.Fatal("scope or digest changed")
			}
			result.Outcome = "ready"
		case "chunk":
			if p.Offset != len(received) {
				t.Fatal("noncontiguous chunks")
			}
			received = append(received, p.Data...)
			result.Outcome, result.Offset = "received", len(received)
		case "commit":
			if !bytes.Equal(data, received) {
				t.Fatal("bytes differ")
			}
			result.Outcome, result.SizeBytes = "completed", len(data)
		}
		foreign, _ := proto.NewEnvelope(proto.TypeWorkspaceWriteResult, uuid.NewString(), result)
		s.dispatch(foreign)
		reply, _ := proto.NewEnvelope(proto.TypeWorkspaceWriteResult, id, result)
		s.dispatch(reply)
		if p.Step == "commit" {
			break
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceWriteDoesNotCommitAfterLostObservation(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
	defer s.Close("test")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := s.WriteWorkspaceFile(ctx, uuid.NewString(), proto.WorkspaceWritePayload{EnvironmentID: uuid.NewString(), SessionID: uuid.NewString(), Path: "file"}, []byte("value"))
		done <- err
	}()
	<-s.sendCh
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case env := <-s.sendCh:
		t.Fatal("lost observer sent extra frame", env.Type)
	default:
	}
}

func TestWorkspaceWriteRejectsPrematureOrContradictoryReceipts(t *testing.T) {
	for _, result := range []proto.WorkspaceWriteResultPayload{
		{Outcome: "completed", SizeBytes: 4},
		{Outcome: "ready", SizeBytes: 4},
		{Outcome: "ready", Offset: 1},
		{Outcome: "rejected", ErrorCode: "private-detail"},
		{Outcome: "unknown", ErrorCode: "write_unconfirmed", SizeBytes: 4},
		// A destination reason refines only a known installer rejection.
		{Outcome: "ready", Reason: proto.WorkspaceWriteReasonUnsafe},
		{Outcome: "rejected", ErrorCode: "invalid_request", Reason: proto.WorkspaceWriteReasonUnsafe},
		{Outcome: "unknown", ErrorCode: "write_unconfirmed", Reason: proto.WorkspaceWriteReasonDirectory},
	} {
		if validWorkspaceWriteResult(result, "ready", 0, 4) {
			t.Fatal("unsafe result", result)
		}
	}
	if validWorkspaceWriteResult(proto.WorkspaceWriteResultPayload{Outcome: "completed", SizeBytes: 4, Reason: proto.WorkspaceWriteReasonUnsafe}, "completed", 0, 4) {
		t.Fatal("reason accepted on a commit")
	}
	// Unrecognized reasons from newer daemons remain generic known rejections.
	for _, reason := range []string{"", proto.WorkspaceWriteReasonDirectory, proto.WorkspaceWriteReasonUnsafe, "future_reason"} {
		if !validWorkspaceWriteResult(proto.WorkspaceWriteResultPayload{Outcome: "rejected", ErrorCode: "write_rejected", Reason: reason}, "completed", 0, 4) {
			t.Fatal("known rejection refused", reason)
		}
	}
	if validWorkspaceWriteResult(proto.WorkspaceWriteResultPayload{Outcome: "received", Offset: 1}, "received", 2, 4) {
		t.Fatal("wrong offset accepted")
	}
}
