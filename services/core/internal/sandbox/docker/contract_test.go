package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/contracttest"
	"github.com/google/uuid"
	"github.com/moby/moby/client"
)

// The script covers only native calls before a fault. Full Docker creation and
// bootstrap remain covered by the existing opt-in lifecycle and recovery tests.
type contractStep struct {
	method, path string
	status       int
	body         any
	fault        contracttest.Fault
}

func dockerContractFixture(t *testing.T, cancel context.CancelFunc, script func(*Provider, sandbox.Reference) []contractStep) contracttest.Fixture {
	t.Helper()
	b := sandbox.Bootstrap{Reference: sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}, SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "https://core.example/api/v1", Credential: "synthetic", NetworkAccess: "enabled"}
	var mu sync.Mutex
	var calls []string
	var steps []contractStep
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1.52")
		call := r.Method + " " + path
		mu.Lock()
		index := len(calls)
		calls = append(calls, call)
		mu.Unlock()
		if index >= len(steps) || call != steps[index].method+" "+steps[index].path {
			t.Errorf("unexpected native request: %s", call)
			http.Error(w, "unexpected request", 500)
			return
		}
		step := steps[index]
		switch step.fault {
		case contracttest.Canceled:
			cancel()
			return
		case contracttest.TransportFailure:
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(step.status)
		if step.body != nil {
			_ = json.NewEncoder(w).Encode(step.body)
		}
	}))
	t.Cleanup(server.Close)
	c, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.52"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	p, err := New(c, Config{InstallationID: uuid.NewString(), Image: "sha256:" + strings.Repeat("a", 64), Network: "bridge", Seccomp: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	steps = script(p, b.Reference)
	want := make([]string, len(steps))
	for i, step := range steps {
		want[i] = step.method + " " + step.path
	}
	return contracttest.Fixture{Provider: p, Bootstrap: b, Calls: func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), calls...)
	}, WantCalls: want}
}

func TestProviderContract(t *testing.T) {
	contracttest.RunFailures(t, func(t *testing.T, s contracttest.Scenario, cancel context.CancelFunc) contracttest.Fixture {
		return dockerContractFixture(t, cancel, func(p *Provider, r sandbox.Reference) []contractStep {
			inspect := "/containers/" + p.name(r) + "/json"
			home, environment := "/volumes/"+p.name(r)+"-home", "/volumes/"+p.name(r)+"-environment"
			missing := map[string]string{"message": "not found"}
			owned := map[string]any{"Id": "native-owned", "Config": map[string]any{"Labels": p.labels(r)}, "State": map[string]string{"Status": "running"}}
			foreign := map[string]any{"Labels": map[string]string{"io.oac.tenant": uuid.NewString()}}
			if s.Fault == contracttest.ForeignOwnership {
				if s.Operation == "kill" {
					// Verify every ownership check precedes any deletion, including volumes.
					return []contractStep{{http.MethodGet, inspect, 200, owned, ""}, {http.MethodGet, home, 200, foreign, ""}}
				}
				return []contractStep{{http.MethodGet, inspect, 200, map[string]any{"Id": "foreign", "Config": foreign}, ""}}
			}
			switch s.Operation {
			case "create":
				return []contractStep{
					{http.MethodGet, inspect, 404, missing, ""},
					{http.MethodGet, home, 404, missing, ""},
					{http.MethodGet, environment, 404, missing, ""},
					{http.MethodPost, "/volumes/create", 0, nil, s.Fault},
				}
			case "kill":
				volume := map[string]any{"Labels": p.labels(r)}
				steps := []contractStep{{http.MethodGet, inspect, 200, owned, ""}, {http.MethodGet, home, 200, volume, ""}, {http.MethodGet, environment, 200, volume, ""}}
				if s.Fault == contracttest.CleanupFailure {
					return append(steps, contractStep{http.MethodDelete, "/containers/native-owned", 204, nil, ""}, contractStep{http.MethodDelete, home, 500, map[string]string{"message": "cleanup failed"}, ""})
				}
				return append(steps, contractStep{http.MethodDelete, "/containers/native-owned", 0, nil, s.Fault})
			default:
				return []contractStep{{http.MethodGet, inspect, 0, nil, s.Fault}}
			}
		})
	})
}

func TestProviderContractObservation(t *testing.T) {
	for _, operation := range []string{"inspect", "renew"} {
		t.Run(operation, func(t *testing.T) {
			f := dockerContractFixture(t, func() {}, func(p *Provider, r sandbox.Reference) []contractStep {
				return []contractStep{{http.MethodGet, "/containers/" + p.name(r) + "/json", 200, map[string]any{"Id": "native-owned", "Config": map[string]any{"Labels": p.labels(r)}, "State": map[string]string{"Status": "exited"}}, ""}}
			})
			var got sandbox.Info
			var err error
			if operation == "renew" {
				got, err = f.Provider.Renew(t.Context(), f.Bootstrap.Reference)
			} else {
				got, err = f.Provider.GetInfo(t.Context(), f.Bootstrap.Reference)
			}
			contracttest.AssertObservation(t, got, err, f.Bootstrap.Reference, "native-owned", "exited")
		})
	}
}
