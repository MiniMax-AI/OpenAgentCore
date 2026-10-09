//go:build linux

package agenthost

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processbroker"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

func TestStdioMCPRequiresDeclaredStrongScope(t *testing.T) {
	for _, strong := range []bool{false, true} {
		t.Run(map[bool]string{false: "no delegation", true: "delegated"}[strong], func(t *testing.T) {
			auth := sandboxlinktest.NewAuthority()
			relay := sandboxlinktest.StartRelay(t, auth)
			binding, instance := newBinding(newResource()), sandboxwire.NewID()
			cfg := Config{RelayURL: relay.URL, TLS: relay.TLS, RuntimeID: sandboxwire.NewID(), Credential: []byte("runtime")}
			auth.AddRuntime(cfg.Credential, cfg.RuntimeID)
			auth.AddServe([]byte("sandbox"), sandboxlink.ServePeer{PeerID: sandboxwire.NewID(), Resource: binding.Resource})
			auth.AddGrant(binding.AttachGrant, sandboxlinktest.Grant{RuntimeID: cfg.RuntimeID, Resource: binding.Resource, SessionID: binding.SessionID,
				AssignmentID: binding.AssignmentID, AssignmentEpoch: binding.AssignmentEpoch, Services: []sandboxlink.Service{sandboxlink.ServiceProcess}, Lease: time.Minute})
			ready, ended := make(chan struct{}), make(chan error, 1)
			ctx, cancel := context.WithCancel(t.Context())
			go func() {
				ended <- sandboxlink.Serve(ctx, sandboxlink.ServeConfig{URL: relay.URL, TLS: relay.TLS, Credential: []byte("sandbox"), Resource: binding.Resource,
					ServerInstanceID: instance, OnConnected: func() { close(ready) }, Services: []sandboxlink.ServiceHandler{{Service: sandboxlink.ServiceProcess, Version: sp.Version,
						Serve: func(_ context.Context, _ sandboxlink.Bind, _ uint64, stream sandboxlink.Stream) {
							defer stream.Close()
							frame, err := sandboxwire.ReadFrame(stream, sandboxwire.MaxPayload)
							if err != nil || frame.Type != sp.OpDescribe {
								return
							}
							scopes := []sp.Scope{sp.ScopePOSIXSession}
							if strong {
								scopes = append(scopes, sp.ScopeCgroupV2)
							}
							response := sp.DescribeResponse{ServerInstanceID: instance, Capabilities: sp.Capabilities{Platform: sp.PlatformLinux, Scopes: scopes, IOModes: []sp.IOMode{sp.IOPipes},
								Signals: []sp.Signal{15}, SignalTargets: []sp.SignalTarget{sp.TargetInitialProcessGroup}, MaxStartBytes: sandboxwire.MaxPayload,
								MaxDataBytes: sandboxwire.MaxChunk, MaxActiveOperations: 8, MaxOperationRecords: 8, MaxReplayBytesPerOperation: 1 << 20,
								OwnerLossGraceMillis: 60000, CancelGraceLimitMillis: 60000}}
							_ = sandboxwire.WriteFrame(stream, sandboxwire.Frame{Type: response.MessageType(), RequestID: frame.RequestID, Payload: sp.Encode(response)})
						}}}})
			}()
			t.Cleanup(func() { cancel(); <-ended })
			select {
			case <-ready:
			case <-time.After(5 * time.Second):
				t.Fatal("serve peer did not connect")
			}
			s, _ := newOwnerSession(t)
			s.plan = &plan{executables: processbroker.Executables{Aliases: map[string]processbroker.Command{"a": {Executable: "/server", Dir: "/"}}}}
			s.link = newLinkOwner(relayDial(cfg), binding, sandboxwire.NewID(), s.fail)
			t.Cleanup(func() { _ = s.link.close() })
			scope, err := s.processScope(t.Context())
			if strong {
				if err != nil || scope != sp.ScopeCgroupV2 {
					t.Fatalf("scope %v: %v", scope, err)
				}
			} else if !errors.Is(err, agent.ErrUnsupportedOperation) || !errors.Is(err, ErrUnsupported) {
				t.Fatalf("stdio admitted without strong containment: scope %v, %v", scope, err)
			}
		})
	}
}

func TestStopMCPRejectsUndeclaredAndHTTPBindings(t *testing.T) {
	s := &session{plan: &plan{mcp: []agent.MCPBinding{{ServerLabel: "stdio", Transport: "stdio"}, {ServerLabel: "http", Transport: "http"}}}}
	for _, label := range []string{"unknown", "http"} {
		if err := s.stopMCP(t.Context(), []string{label}); !errors.Is(err, agent.ErrUnsupportedOperation) {
			t.Fatalf("%s: %v", label, err)
		}
	}
	if err := s.stopMCP(t.Context(), []string{"stdio"}); !errors.Is(err, agent.ErrNoLiveView) {
		t.Fatalf("missing live broker: %v", err)
	}
}
