package runtimegateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func expectCapabilityHint(t *testing.T, reg *Registry, want bool) {
	t.Helper()
	select {
	case <-reg.CapabilityHints():
		if !want {
			t.Fatal("unexpected capability hint")
		}
	default:
		if want {
			t.Fatal("missing capability hint")
		}
	}
}

func TestCapabilityHintsRequireCurrentAvailableSnapshot(t *testing.T) {
	reg := NewRegistry()
	old := NewSession(newFakeConn(), "device", "tenant", "version", reg, nil)
	reg.Register(old)
	expectCapabilityHint(t, reg, false) // Transport alone is insufficient.
	publishCapabilityHeartbeat(t, old, []runtimedevice.SupportedAgentKind{{Kind: "fake", Available: false}})
	expectCapabilityHint(t, reg, false)
	kinds := []runtimedevice.SupportedAgentKind{{Kind: "fake", Available: true}}
	publishCapabilityHeartbeat(t, old, kinds)
	expectCapabilityHint(t, reg, true)
	publishCapabilityHeartbeat(t, old, kinds)
	expectCapabilityHint(t, reg, false) // Ordinary heartbeats do not rescan.
	newer := NewSession(newFakeConn(), "device", "tenant", "version", reg, nil)
	reg.Register(newer)
	kinds[0].Version = "changed"
	publishCapabilityHeartbeat(t, old, kinds)
	expectCapabilityHint(t, reg, false) // Superseded socket cannot accelerate work.
	publishCapabilityHeartbeat(t, newer, kinds)
	expectCapabilityHint(t, reg, true)
}

func TestCapabilityHintsCoalesceAndIgnoreDisconnectedPeers(t *testing.T) {
	reg := NewRegistry()
	s := NewSession(newFakeConn(), "device", "tenant", "version", reg, nil)
	reg.Register(s)
	kinds := []runtimedevice.SupportedAgentKind{{Kind: "fake", Available: true}}
	publishCapabilityHeartbeat(t, s, kinds)
	kinds[0].Capabilities.Streaming = true
	publishCapabilityHeartbeat(t, s, kinds)
	expectCapabilityHint(t, reg, true)
	expectCapabilityHint(t, reg, false)
	reg.Deregister(s)
	kinds[0].Capabilities.Steering = true
	publishCapabilityHeartbeat(t, s, kinds)
	expectCapabilityHint(t, reg, false)
}

func TestInvalidHeartbeatCannotWakeScheduler(t *testing.T) {
	reg := NewRegistry()
	s := NewSession(newFakeConn(), "device", "tenant", "version", reg, nil)
	reg.Register(s)
	s.handleHeartbeat(proto.Envelope{Type: proto.TypeHeartbeat, Payload: json.RawMessage(`{"ts":1,"active_requests":0,"supported_agent_kinds":[{"kind":"fake","available":true,"capabilities":{"streaming":"invented"}}]}`)})
	expectCapabilityHint(t, reg, false)
	if !s.IsClosed() {
		t.Fatal("malformed capabilities did not close transport")
	}
}

func publishCapabilityHeartbeat(t *testing.T, s *Session, kinds []runtimedevice.SupportedAgentKind) {
	t.Helper()
	advertised := make([]proto.SupportedAgentKind, len(kinds))
	for i, k := range kinds {
		advertised[i] = proto.SupportedAgentKind{Kind: k.Kind, Available: k.Available, Version: k.Version,
			Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
				Streaming: proto.CapabilityFromBool(k.Capabilities.Streaming),
				Steering:  proto.CapabilityFromBool(k.Capabilities.Steering),
			})}
	}
	env, err := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{SupportedAgentKinds: advertised})
	if err != nil {
		t.Fatal(err)
	}
	s.handleHeartbeat(env)
}

type capabilityAuthorityStore struct {
	HeartbeatTouch
	deleted bool
	err     error
}

func (a *capabilityAuthorityStore) TouchAgentDaemonHeartbeat(context.Context, runtimedevice.Heartbeat) (runtimedevice.HeartbeatStatus, error) {
	return runtimedevice.HeartbeatStatus{Deleted: a.deleted}, a.err
}
func TestCapabilityConfirmationRetriesWithoutObservingRejectedAuthority(t *testing.T) {
	for _, mode := range []string{"error", "deleted", "draining"} {
		t.Run(mode, func(t *testing.T) {
			reg := NewRegistry()
			s := NewSession(newFakeConn(), "device", "tenant", "version", reg, nil)
			reg.Register(s)
			t.Cleanup(func() { s.Close("test finished") })
			authority := &capabilityAuthorityStore{deleted: mode != "error"}
			if mode == "error" {
				authority.err = errors.New("private credential must not be observed")
			}
			s.heartbeat = authority
			if mode == "draining" {
				s.credentialHash = "original-hash"
				s.archivedCancellations = &receiptStore{receipt: runtimedevice.ArchivedCancellationReceipt{RunID: "run", Deadline: time.Now().Add(time.Minute)}}
				release, err := s.TrackExecutionDelivery("run")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(release)
			}
			kinds := []runtimedevice.SupportedAgentKind{{Kind: "fake", Available: true}}
			publishCapabilityHeartbeat(t, s, kinds)
			expectCapabilityHint(t, reg, false)
			if mode == "draining" && s.IsClosed() {
				t.Fatal("existing receipt drain was interrupted")
			}
			if mode == "error" {
				authority.err = nil
				publishCapabilityHeartbeat(t, s, kinds)
				expectCapabilityHint(t, reg, true)
				publishCapabilityHeartbeat(t, s, kinds)
				expectCapabilityHint(t, reg, false)
			}
		})
	}
}

func TestCapabilityObservationsExcludeRejectedAndStalePeers(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	reg := NewRegistry()
	old := NewSession(newFakeConn(), "device", "tenant", "version", reg, nil)
	reg.Register(old)
	kinds := []runtimedevice.SupportedAgentKind{{Kind: "fake", Available: true}}
	publishCapabilityHeartbeat(t, old, kinds)
	if !strings.Contains(logs.String(), "runtime capability snapshot observed") {
		t.Fatal("valid declaration was not observed")
	}
	logs.Reset()
	old.handleHeartbeat(proto.Envelope{Type: proto.TypeHeartbeat, Payload: json.RawMessage(`{"supported_agent_kinds":[{"kind":"fake","available":true,"capabilities":{"streaming":"invented"}}]}`)})
	if logs.Len() != 0 {
		t.Fatal("invalid declaration logged as an accepted snapshot")
	}
	newer := NewSession(newFakeConn(), "device", "tenant", "version", reg, nil)
	reg.Register(newer)
	publishCapabilityHeartbeat(t, old, kinds)
	if logs.Len() != 0 {
		t.Fatal("superseded connection logged as an accepted snapshot")
	}
	publishCapabilityHeartbeat(t, newer, kinds)
	if !strings.Contains(logs.String(), "runtime capability snapshot observed") {
		t.Fatal("replacement declaration was not observed")
	}
	logs.Reset()
	reg.Deregister(newer)
	kinds[0].Version = "changed"
	publishCapabilityHeartbeat(t, newer, kinds)
	if logs.Len() != 0 {
		t.Fatal("disconnected connection logged as an accepted snapshot")
	}
}
