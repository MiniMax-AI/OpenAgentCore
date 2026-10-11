package smolvm

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/contracttest"
	"github.com/google/uuid"
	sdk "github.com/smol-machines/smolvm-sdk/smolvm-go"
)

type nativeStep struct {
	method, path string
	status       int
	body         any
	fault        contracttest.Fault
}

func TestProviderFailures(t *testing.T) {
	contracttest.RunFailures(t, func(t *testing.T, scenario contracttest.Scenario, cancel context.CancelFunc) contracttest.Fixture {
		var mutex sync.Mutex
		var calls []string
		var steps []nativeStep
		p, ref, _ := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
			call := r.Method + " " + r.URL.Path
			mutex.Lock()
			index := len(calls)
			calls = append(calls, call)
			mutex.Unlock()
			if index >= len(steps) || call != steps[index].method+" "+steps[index].path {
				t.Errorf("unexpected native request %q", call)
				http.Error(w, "unexpected", 500)
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
		})
		bootstrap := sandbox.Bootstrap{Reference: ref, SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "https://core.example/api/v1", Credential: "test", NetworkAccess: "enabled"}
		name := p.name(ref)
		get := "/api/v1/machines/" + name
		owned := sdk.MachineInfo{Name: name, Image: p.imageRef, Labels: p.labels(ref, uuid.NewString()), State: sdk.MachineStateRunning, CPUs: 2, MemoryMB: 1024}
		foreign := owned
		foreign.Labels = p.labels(ref, uuid.NewString())
		foreign.Labels[labelPrefix+"tenant"] = "foreign"
		absent := map[string]string{"error": "not found", "code": "NOT_FOUND"}
		switch scenario.Operation {
		case "create":
			if scenario.Fault == contracttest.ForeignOwnership {
				steps = []nativeStep{{http.MethodGet, get, 200, foreign, ""}}
			} else {
				steps = []nativeStep{{http.MethodGet, get, 404, absent, ""}, {http.MethodPost, "/api/v1/machines", 200, nil, scenario.Fault}}
			}
		case "inspect", "renew":
			if scenario.Fault == contracttest.ForeignOwnership {
				steps = []nativeStep{{http.MethodGet, get, 200, foreign, ""}}
			} else {
				steps = []nativeStep{{http.MethodGet, get, 200, nil, scenario.Fault}}
			}
		case "kill":
			if scenario.Fault == contracttest.ForeignOwnership {
				steps = []nativeStep{{http.MethodGet, get, 200, foreign, ""}}
			} else {
				steps = []nativeStep{{http.MethodGet, get, 200, owned, ""}, {http.MethodDelete, get, 200, map[string]string{"deleted": name}, scenario.Fault}}
				if scenario.Fault == contracttest.CleanupFailure {
					steps = append(steps, nativeStep{http.MethodGet, get, 200, owned, ""})
				}
			}
		}
		want := make([]string, len(steps))
		for i, step := range steps {
			want[i] = step.method + " " + step.path
		}
		return contracttest.Fixture{Provider: p, Bootstrap: bootstrap,
			Calls: func() []string { mutex.Lock(); defer mutex.Unlock(); return append([]string(nil), calls...) }, WantCalls: want}
	})
}
