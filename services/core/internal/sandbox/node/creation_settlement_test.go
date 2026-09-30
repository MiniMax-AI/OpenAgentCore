package node

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

type settledProvider struct {
	fakeProvider
	info sandbox.Info
	err  error
}

func (p *settledProvider) Create(context.Context, sandbox.Bootstrap) (sandbox.Info, error) {
	return p.info, p.err
}
func (p *settledProvider) GetInfo(context.Context, sandbox.Reference) (sandbox.Info, error) {
	return p.info, p.err
}

func TestNodeCarriesCreationSettlementWithoutConvertingFailureToSuccess(t *testing.T) {
	ref := reference()
	for _, test := range []struct {
		name string
		info sandbox.Info
		err  error
		keep bool
	}{
		{"rejected before bootstrap", sandbox.Info{Reference: ref, ProviderID: "owned", CreateSettled: true}, sandbox.ErrInvalid, true},
		{"confirmed absent", sandbox.Info{Reference: ref, State: "absent", CreateSettled: true}, sandbox.ErrInvalid, true},
		{"observed absent", sandbox.Info{Reference: ref, State: "absent", CreateSettled: true}, nil, true},
		{"unsettled error", sandbox.Info{Reference: ref, ProviderID: "owned"}, sandbox.ErrInvalid, false},
		{"foreign settlement", sandbox.Info{Reference: reference(), ProviderID: "foreign", CreateSettled: true}, sandbox.ErrInvalid, false},
		{"absent but ready", sandbox.Info{Reference: ref, State: "absent", CreateSettled: true, BootstrapComplete: true}, sandbox.ErrInvalid, false},
		{"missing compute identity", sandbox.Info{Reference: ref, CreateSettled: true}, sandbox.ErrInvalid, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			id := identity()
			hub := NewHub(HubOptions{
				Authenticate: func(context.Context, string, string) (Identity, error) { return id, nil },
				OwnerEpoch:   func(context.Context) (uint64, error) { return 1, nil },
			})
			defer hub.Close()
			server := httptest.NewServer(hub)
			defer server.Close()
			dir := stateDir(t)
			stored, err := InitIdentity(dir, server.URL, id)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() {
				done <- Run(ctx, AgentConfig{CoreURL: server.URL, StateDirectory: dir, Identity: id, Credential: stored.Credential,
					Provider: &settledProvider{info: test.info, err: test.err}, Probe: probe})
			}()
			defer func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(5 * time.Second):
					t.Error("node did not stop")
				}
			}()
			wait(t, func() bool { return hub.Online(id.NodeID) })
			proxy := hub.Proxy(id.NodeID, "docker", 1)
			for _, operation := range []func(context.Context) (sandbox.Info, error){
				func(ctx context.Context) (sandbox.Info, error) {
					return proxy.Create(ctx, sandbox.Bootstrap{Reference: ref})
				},
				func(ctx context.Context) (sandbox.Info, error) { return proxy.GetInfo(ctx, ref) },
			} {
				call, stop := context.WithTimeout(ctx, 3*time.Second)
				info, err := operation(call)
				stop()
				if !errors.Is(err, test.err) {
					t.Fatalf("operation error changed: %v", err)
				}
				if test.keep && info != test.info || !test.keep && info != (sandbox.Info{}) {
					t.Fatalf("incorrect settlement forwarding: %+v", info)
				}
			}
		})
	}
}
