package cli

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/auth"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/transport"
)

func awaitStartup(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("startup worker did not reach barrier")
	}
}

func TestConnectionPreflightOverlapsAndJoins(t *testing.T) {
	for _, first := range []string{"bootstrap", "discovery"} {
		t.Run(first, func(t *testing.T) {
			discoveryStarted, bootstrapStarted := make(chan struct{}), make(chan struct{})
			releaseDiscovery, releaseBootstrap := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			boot := &transport.BootstrapResponse{DeviceID: "device"}
			go func() {
				result, _, err := prepareConnection(t.Context(), func(context.Context) (agentCLIDiscovery, error) {
					close(discoveryStarted)
					<-releaseDiscovery
					return agentCLIDiscovery{}, nil
				}, func(context.Context) (*transport.BootstrapResponse, error) {
					close(bootstrapStarted)
					<-releaseBootstrap
					return boot, nil
				})
				if err == nil && result != boot {
					err = errors.New("bootstrap result lost")
				}
				done <- err
			}()
			awaitStartup(t, discoveryStarted)
			awaitStartup(t, bootstrapStarted)
			if first == "bootstrap" {
				close(releaseBootstrap)
			} else {
				close(releaseDiscovery)
			}
			select {
			case <-done:
				t.Fatal("returned before both prerequisites completed")
			default:
			}
			if first == "bootstrap" {
				close(releaseDiscovery)
			} else {
				close(releaseBootstrap)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("join blocked")
			}
		})
	}
}

func TestConnectionPreflightFailureCancelsAndJoinsSibling(t *testing.T) {
	for _, failing := range []string{"bootstrap", "discovery"} {
		t.Run(failing, func(t *testing.T) {
			cause := errors.New("rejected")
			started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			wait := func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				close(canceled)
				<-release
				return ctx.Err()
			}
			fail := func() error { <-started; return cause }
			go func() {
				boot, agents, err := prepareConnection(t.Context(), func(ctx context.Context) (agentCLIDiscovery, error) {
					if failing == "discovery" {
						return nil, fail()
					}
					return nil, wait(ctx)
				}, func(ctx context.Context) (*transport.BootstrapResponse, error) {
					if failing == "bootstrap" {
						return nil, fail()
					}
					return nil, wait(ctx)
				})
				if boot != nil || agents != nil {
					err = errors.New("partial successful result escaped")
				}
				done <- err
			}()
			awaitStartup(t, canceled)
			select {
			case <-done:
				t.Fatal("returned before sibling cleanup")
			default:
			}
			close(release)
			select {
			case err := <-done:
				if !errors.Is(err, cause) {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("join blocked")
			}
		})
	}
}

func TestConnectionPreflightParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{}, 2)
	done := make(chan error, 1)
	go func() {
		_, _, err := prepareConnection(ctx, func(ctx context.Context) (agentCLIDiscovery, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		}, func(ctx context.Context) (*transport.BootstrapResponse, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		})
		done <- err
	}()
	awaitStartup(t, started)
	awaitStartup(t, started)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation blocked")
	}
	_, _, err := prepareConnection(ctx, func(context.Context) (agentCLIDiscovery, error) {
		t.Error("discovery started after cancellation")
		return nil, nil
	}, func(context.Context) (*transport.BootstrapResponse, error) {
		t.Error("bootstrap started after cancellation")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// Controlled waits model independent work, not production latency. Compare the
// same two operations to demonstrate the removed dependency edge.
func BenchmarkConnectionPreflight(b *testing.B) {
	for _, parallel := range []bool{false, true} {
		name := "serial"
		if parallel {
			name = "overlap"
		}
		b.Run(name, func(b *testing.B) {
			discover := func(context.Context) (agentCLIDiscovery, error) {
				time.Sleep(5 * time.Millisecond)
				return agentCLIDiscovery{}, nil
			}
			bootstrap := func(context.Context) (*transport.BootstrapResponse, error) {
				time.Sleep(5 * time.Millisecond)
				return &transport.BootstrapResponse{}, nil
			}
			for b.Loop() {
				if parallel {
					_, _, _ = prepareConnection(b.Context(), discover, bootstrap)
				} else {
					_, _ = discover(b.Context())
					_, _ = bootstrap(b.Context())
				}
			}
		})
	}
}

func TestMainLoopOverlapsPreflightWithoutEarlyRegistration(t *testing.T) {
	for _, failure := range []string{"", "discovery", "bootstrap"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("OAC_RUNTIME_DAEMON_SUSPEND_PID_FILE", "")
			original := harnessDeclarations
			defer func() { harnessDeclarations = original }()
			started, release, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var probes, boots, dials atomic.Int32
			declaration := original[0]
			declaration.Discover = func(ctx context.Context, _ agent.DiscoveryOptions, info proto.SupportedAgentKind) *agent.Runtime {
				probes.Add(1)
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
				}
				defer close(completed)
				info.Available = failure != "discovery" && ctx.Err() == nil
				return &agent.Runtime{Info: info}
			}
			harnessDeclarations = []agent.Declaration{declaration}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/agent-daemon/bootstrap":
					boots.Add(1)
					select {
					case <-started:
					case <-r.Context().Done():
						return
					}
					if dials.Load() != 0 {
						t.Error("dial before discovery completed")
					}
					close(release)
					if failure == "bootstrap" {
						http.Error(w, "rejected", http.StatusUnauthorized)
						return
					}
					_ = json.NewEncoder(w).Encode(transport.BootstrapResponse{DeviceID: "device"})
				case "/agent-daemon/ws":
					select {
					case <-completed:
					default:
						t.Error("registration before discovery joined")
					}
					dials.Add(1)
					http.Error(w, "end test", http.StatusUnauthorized)
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			err := mainLoopRemote(ctx, &runContext{stdout: io.Discard, stderr: io.Discard}, "default", auth.Profile{ServerURL: server.URL, RuntimeID: "device", RunnerCredential: "fixture"}, "")
			if err == nil || ctx.Err() != nil {
				t.Fatalf("unexpected completion: %v, context %v", err, ctx.Err())
			}
			expected := int32(0)
			if failure == "" {
				expected = 1
			}
			if probes.Load() != 1 || boots.Load() != 1 || dials.Load() != expected {
				t.Fatalf("calls discovery=%d bootstrap=%d dial=%d", probes.Load(), boots.Load(), dials.Load())
			}
		})
	}
}
