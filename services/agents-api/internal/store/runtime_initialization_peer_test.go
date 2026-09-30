package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/gorilla/websocket"
)

// initializationPeer exercises the real authenticated gateway and chunk receipts.
// The provider fixture bootstraps its socket; all initialization runs on that peer.
type initializationPeer struct {
	t            *testing.T
	endpoint     string
	registry     *gateway.Registry
	apply        func(proto.RuntimePreparePayload, []byte) proto.RuntimePrepareResultPayload
	writes       atomic.Int32
	commandCalls atomic.Int32
	deferred     bool
	unavailable  bool
	bootstrap    sandbox.Bootstrap
}

func (p *initializationPeer) setRuntimeGateway(t *testing.T, endpoint string, registry *gateway.Registry) {
	p.t, p.endpoint, p.registry = t, endpoint, registry
}
func (p *initializationPeer) connect(b sandbox.Bootstrap) error {
	p.bootstrap = b
	if p.deferred {
		return nil
	}
	c, _, err := websocket.DefaultDialer.Dial(p.endpoint+"?device_id="+b.DeviceID+"&version="+proto.Version, http.Header{"Authorization": {"Bearer " + b.Credential}})
	if err != nil {
		return err
	}
	p.t.Cleanup(func() { _ = c.Close() })
	heartbeat, _ := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: !p.unavailable, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}}})
	if err := c.WriteJSON(heartbeat); err != nil {
		return err
	}

	go func() {
		transfer := initializationTransfer{peer: p}
		for {
			var env proto.Envelope
			if c.ReadJSON(&env) != nil {
				return
			}
			if env.Type != proto.TypeRuntimePrepare {
				continue
			}
			reply, err := transfer.receive(env)
			if err != nil {
				p.t.Error(err)
				return
			}
			if c.WriteJSON(reply) != nil {
				return
			}
		}
	}()
	end := time.Now().Add(time.Second)
	for time.Now().Before(end) {
		if _, err := p.registry.LookupDevice(b.DeviceID); err == nil {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return context.DeadlineExceeded
}
func (p *initializationPeer) RunCommand(context.Context, sandbox.Reference, sandbox.Command) (sandbox.CommandResult, error) {
	p.commandCalls.Add(1)
	return sandbox.CommandResult{}, sandbox.ErrInvalid
}
func completedInitialization(proto.RuntimePreparePayload, []byte) proto.RuntimePrepareResultPayload {
	return proto.RuntimePrepareResultPayload{Outcome: "completed"}
}

// Each authenticated socket owns its own bounded preparation transfer.
type initializationTransfer struct {
	peer    *initializationPeer
	request proto.RuntimePreparePayload
	data    []byte
}

func (x *initializationTransfer) receive(env proto.Envelope) (proto.Envelope, error) {
	var frame proto.RuntimePreparePayload
	if env.DecodePayload(&frame) != nil || !proto.ValidRuntimePrepareRequest(frame) {
		return proto.Envelope{}, errors.New("invalid Runtime frame")
	}
	result := proto.RuntimePrepareResultPayload{}
	switch frame.Step {
	case "begin":
		x.request, x.data = frame, nil
		result.Outcome = "ready"
	case "chunk":
		if frame.Offset != len(x.data) {
			return proto.Envelope{}, errors.New("unordered initialization bytes")
		}
		x.data = append(x.data, frame.Data...)
		result.Outcome, result.Offset = "received", len(x.data)
	case "commit":
		if x.request.Action == "file" || x.request.Action == "skill" || x.request.Action == "plugin" {
			sum := sha256.Sum256(x.data)
			if x.request.SizeBytes != len(x.data) || x.request.SHA256 != hex.EncodeToString(sum[:]) {
				return proto.Envelope{}, errors.New("initialization digest changed")
			}
		}
		result = x.peer.apply(x.request, x.data)
		x.peer.writes.Add(1)
		if result.Outcome == "completed" {
			result.SizeBytes = len(x.data)
		}
	}
	return proto.NewEnvelope(proto.TypeRuntimePrepareResult, env.ID, result)
}
