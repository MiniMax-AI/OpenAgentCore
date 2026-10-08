package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/gorilla/websocket"
)

// initializationPeer exercises the real authenticated gateway and chunk
// receipts. It connects as the deployment's agent host, on which all
// initialization runs, and has a fake sandbox Serve each bootstrap's Link
// resource at link.
type initializationPeer struct {
	t           *testing.T
	endpoint    string
	registry    *runtimegateway.Registry
	link        *sandboxlinktest.Server
	host        agentHost
	tenant      string                // scopes the agent host setRuntimeGateway registers; see registerAgentHost
	serving     map[string]*linkServe // by Link resource ID
	apply       func(proto.RuntimePreparePayload, []byte) proto.RuntimePrepareResultPayload
	writes      atomic.Int32
	deferred    bool
	unavailable bool
	bootstrap   sandbox.Bootstrap
	binds       chan proto.AssignmentBindPayload // when not nil, receives each bind's payload
	closeOnBind bool                             // close the socket at a bind instead of replying
}

// setRuntimeGateway points the peer at the gateway and the relay, and
// registers its agent host on s unless the peer already has one.
func (p *initializationPeer) setRuntimeGateway(t *testing.T, s *Store, endpoint string, registry *runtimegateway.Registry, link *sandboxlinktest.Server) {
	p.t, p.endpoint, p.registry, p.link, p.serving = t, endpoint, registry, link, nil
	if p.host.ID == "" {
		p.host = registerAgentHost(t, s, p.tenant)
	}
}

// connect has a fake sandbox Serve the bootstrap's Link resource, once per
// resource, and connects the agent host again.
func (p *initializationPeer) connect(b sandbox.Bootstrap) error {
	p.bootstrap = b
	if p.deferred {
		return nil
	}
	if resource := b.SandboxIO.Resource; p.link != nil && b.SandboxIO.Credential != "" && p.serving[resource.ID] == nil {
		served := startLinkServe(p.t, p.link, []byte(b.SandboxIO.Credential), resource.Ref())
		select {
		case <-served.connected:
		case <-time.After(linkWait):
			return context.DeadlineExceeded
		}
		if p.serving == nil {
			p.serving = map[string]*linkServe{}
		}
		p.serving[resource.ID] = served
	}
	c, _, err := websocket.DefaultDialer.Dial(p.endpoint+"?device_id="+p.host.ID+"&version="+proto.Version, http.Header{"Authorization": {"Bearer " + p.host.Credential}})
	if err != nil {
		return err
	}
	p.t.Cleanup(func() { _ = c.Close() })
	heartbeat, _ := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{HomeRemoval: proto.CapabilityUnsupported, SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: !p.unavailable, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}}})
	if err := c.WriteJSON(heartbeat); err != nil {
		return err
	}

	go func() {
		transfer := newInitializationTransfer(p, c)
		for {
			var env proto.Envelope
			if c.ReadJSON(&env) != nil {
				return
			}
			if reply, ok := assignmentReply(env); ok {
				var bind proto.AssignmentBindPayload
				if p.binds != nil && env.DecodePayload(&bind) == nil {
					p.binds <- bind
				}
				if p.closeOnBind {
					_ = c.Close()
					return
				}
				if transfer.write(reply) != nil {
					return
				}
				continue
			}
			if env.Type != proto.TypeRuntimePrepare {
				continue
			}
			if err := transfer.receive(env); err != nil {
				p.t.Error(err)
				return
			}
		}
	}()
	end := time.Now().Add(time.Second)
	for time.Now().Before(end) {
		if _, err := p.registry.LookupDevice(p.host.ID); err == nil {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return context.DeadlineExceeded
}
func completedInitialization(proto.RuntimePreparePayload, []byte) proto.RuntimePrepareResultPayload {
	return proto.RuntimePrepareResultPayload{Outcome: "completed"}
}

// initializationTransfer answers preparation frames on one agent host socket,
// which initializes many Environments. Every step of one preparation shares
// its envelope ID. A commit applies in the background, as the Runtime does, so
// a blocked preparation holds up no other Session's.
type initializationTransfer struct {
	peer    *initializationPeer
	conn    *websocket.Conn
	writeMu sync.Mutex
	pending map[string]*preparationTransfer // by envelope ID
}

type preparationTransfer struct {
	request proto.RuntimePreparePayload
	data    []byte
}

func newInitializationTransfer(peer *initializationPeer, conn *websocket.Conn) *initializationTransfer {
	return &initializationTransfer{peer: peer, conn: conn, pending: map[string]*preparationTransfer{}}
}

func (x *initializationTransfer) write(env proto.Envelope) error {
	x.writeMu.Lock()
	defer x.writeMu.Unlock()
	return x.conn.WriteJSON(env)
}

func (x *initializationTransfer) receive(env proto.Envelope) error {
	var frame proto.RuntimePreparePayload
	if env.DecodePayload(&frame) != nil || !proto.ValidRuntimePrepareRequest(frame) {
		return errors.New("invalid Runtime frame")
	}
	if frame.Step == "begin" {
		x.pending[env.ID] = &preparationTransfer{request: frame}
	}
	current := x.pending[env.ID]
	if current == nil {
		return errors.New("initialization step without begin")
	}
	result := proto.RuntimePrepareResultPayload{}
	switch frame.Step {
	case "begin":
		result.Outcome = "ready"
	case "chunk":
		if frame.Offset != len(current.data) {
			return errors.New("unordered initialization bytes")
		}
		current.data = append(current.data, frame.Data...)
		result.Outcome, result.Offset = "received", len(current.data)
	case "commit":
		if current.request.Action == "file" || current.request.Action == "skill" || current.request.Action == "plugin" {
			sum := sha256.Sum256(current.data)
			if current.request.SizeBytes != len(current.data) || current.request.SHA256 != hex.EncodeToString(sum[:]) {
				return errors.New("initialization digest changed")
			}
		}
		delete(x.pending, env.ID)
		go func() {
			result := x.peer.apply(current.request, current.data)
			x.peer.writes.Add(1)
			if result.Outcome == "completed" {
				result.SizeBytes = len(current.data)
			}
			if reply, err := env.Reply(proto.TypeRuntimePrepareResult, result); err == nil {
				_ = x.write(reply)
			}
		}()
		return nil
	}
	reply, err := env.Reply(proto.TypeRuntimePrepareResult, result)
	if err != nil {
		return err
	}
	return x.write(reply)
}
