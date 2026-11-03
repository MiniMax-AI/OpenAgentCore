package opensandbox

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/contracttest"
)

func TestProviderContract(t *testing.T) {
	contracttest.RunFailures(t, func(t *testing.T, s contracttest.Scenario, cancel context.CancelFunc) contracttest.Fixture {
		f := newFake(t)
		p, r := testProvider(t, f)
		want := []string{"list"}
		switch s.Operation {
		case "create":
			// The fault hits the native create; a foreign item hits the lookup before it.
			if s.Fault != contracttest.ForeignOwnership {
				want = []string{"list", "create"}
			}
		case "kill":
			if s.Fault == contracttest.CleanupFailure {
				want = []string{"list", "delete", "list"}
				f.keepOn = true
				f.add(r, p.installation, nil)
			}
		}
		target := "list"
		if s.Operation == "create" && s.Fault != contracttest.ForeignOwnership {
			target = "create"
		}
		f.fault = func(call string, w http.ResponseWriter) bool {
			if call != target || s.Fault == contracttest.CleanupFailure {
				return false
			}
			switch s.Fault {
			case contracttest.Canceled:
				cancel()
				time.Sleep(50 * time.Millisecond)
			case contracttest.ForeignOwnership:
				writeJSON(w, 200, map[string]any{"items": []map[string]any{{"id": "x", "status": map[string]any{"state": "Running"},
					"metadata": map[string]string{keyInstallation: p.installation, keyTenant: r.TenantID, keyEnvironment: r.EnvironmentID, keyAllocation: "foreign"}}},
					"pagination": map[string]any{"hasNextPage": false}})
				return true
			}
			writeJSON(w, 500, map[string]string{"code": "INTERNAL", "message": "injected"})
			return true
		}
		return contracttest.Fixture{Provider: p, Bootstrap: bootstrap(r), Calls: f.Calls, WantCalls: want}
	})
}

func TestCreateBootstrapsOnce(t *testing.T) {
	f := newFake(t)
	p, r := testProvider(t, f)
	b := bootstrap(r)
	got, err := p.Create(bounded(t), b)
	contracttest.AssertObservation(t, got, err, r, "", "running")
	if !got.BootstrapComplete || !got.CreateSettled {
		t.Fatalf("created sandbox not reported complete: %+v", got)
	}
	if want := []string{"list", "create", "get", "networkpolicy", "endpoint", "execd/files/upload", "execd/command", "patch", "list"}; !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls = %v, want %v", f.calls, want)
	}
	data, _ := b.RuntimeConnection().Marshal()
	if u := f.uploads[bootstrapFile]; u.Data != string(data) || u.Mode != 600 {
		t.Fatalf("bootstrap upload = %+v", u)
	}
	if want := []string{"/usr/local/bin/oac-daemon", "connect", "--profile", "default", "--bootstrap-file", bootstrapFile, "-b"}; !reflect.DeepEqual(f.commands[0], want) || f.cwds[0] != "/environment/workspace" {
		t.Fatalf("daemon command = %v in %s", f.commands[0], f.cwds[0])
	}
	body := f.created
	if body["image"].(map[string]any)["uri"] != "runtime:test" || body["timeout"] != float64(3600) ||
		!reflect.DeepEqual(body["entrypoint"], []any{"sleep", "infinity"}) ||
		!reflect.DeepEqual(body["resourceLimits"], map[string]any{"cpu": "2", "memory": "4096Mi"}) ||
		body["networkPolicy"].(map[string]any)["defaultAction"] != "allow" {
		t.Fatalf("create request = %v", body)
	}
	if _, err := p.Create(bounded(t), b); !errors.Is(err, sandbox.ErrExists) {
		t.Fatalf("second create = %v", err)
	}
}

func TestCreateRejectsEgressWithoutNftEnforcement(t *testing.T) {
	f := newFake(t)
	f.mode = "dns"
	p, r := testProvider(t, f)
	got, err := p.Create(bounded(t), bootstrap(r))
	if !errors.Is(err, sandbox.ErrInvalid) || !got.CreateSettled || got.BootstrapComplete || len(f.uploads) != 0 {
		t.Fatalf("create = %+v, %v", got, err)
	}
}

func TestCreateBeforeDispatchIsAbsent(t *testing.T) {
	f := newFake(t)
	p, r := testProvider(t, f)
	b := bootstrap(r)
	b.Egress.PrivateDomains = []string{"corp.example"}
	got, err := p.Create(bounded(t), b)
	if !errors.Is(err, sandbox.ErrInvalid) || got.State != "absent" || !got.CreateSettled || len(f.Calls()) != 0 {
		t.Fatalf("create = %+v, %v, calls %v", got, err, f.Calls())
	}
}

func TestCreateNonZeroInitializationIsUnconfirmed(t *testing.T) {
	f := newFake(t)
	three := 3
	f.exit = func([]string) (string, string, *int) { return "", "", &three }
	p, r := testProvider(t, f)
	got, err := p.Create(bounded(t), bootstrap(r))
	if !errors.Is(err, sandbox.ErrCommandUnconfirmed) || !got.CreateSettled || got.BootstrapComplete {
		t.Fatalf("create = %+v, %v", got, err)
	}
}

func TestRenewOnlyRunning(t *testing.T) {
	f := newFake(t)
	p, r := testProvider(t, f)
	f.state = "Paused"
	f.add(r, p.installation, nil)
	got, err := p.Renew(bounded(t), r)
	if !errors.Is(err, sandbox.ErrComputeUnconfirmed) || got.State != "paused" || strings.Contains(strings.Join(f.Calls(), ","), "renew") {
		t.Fatalf("renew = %+v, %v, calls %v", got, err, f.Calls())
	}
}

func TestKillRemovesAndConfirms(t *testing.T) {
	f := newFake(t)
	p, r := testProvider(t, f)
	f.add(r, p.installation, nil)
	if err := p.Kill(bounded(t), r); err != nil || !reflect.DeepEqual(f.Calls(), []string{"list", "delete", "list"}) {
		t.Fatalf("kill = %v, calls %v", err, f.Calls())
	}
	if err := p.Kill(bounded(t), r); err != nil {
		t.Fatal("absent allocation:", err)
	}
}

func TestRunCommand(t *testing.T) {
	f := newFake(t)
	p, r := testProvider(t, f)
	f.add(r, p.installation, map[string]string{keyBootstrap: "complete"})
	seven := 7
	f.exit = func([]string) (string, string, *int) { return "out", "err", &seven }
	got, err := p.RunCommand(bounded(t), r, sandbox.Command{Args: []string{"tool", "--flag"}, Directory: "/environment/workspace"})
	if err != nil || got != (sandbox.CommandResult{Stdout: "out", Stderr: "err", ExitCode: 7}) || !reflect.DeepEqual(f.commands[0], []string{"tool", "--flag"}) || f.cwds[0] != "/environment/workspace" {
		t.Fatalf("result = %+v, %v, argv %v", got, err, f.commands)
	}
	if _, err := p.RunCommand(bounded(t), r, sandbox.Command{Args: []string{"tool"}, Stdin: []byte("secret")}); err != nil {
		t.Fatal(err)
	}
	argv := f.commands[1]
	file := argv[4]
	if len(argv) != 6 || argv[0] != "/bin/sh" || argv[3] != "sh" || argv[5] != "tool" || !strings.HasPrefix(file, stdinDir+"/opensandbox-stdin-") ||
		f.uploads[file] != (uploaded{Mode: 600, Data: "secret"}) || strings.Contains(strings.Join(argv, " "), "secret") {
		t.Fatalf("stdin staging: argv %v uploads %v", argv, f.uploads)
	}
}

func TestRunCommandWithoutExitCodeIsUnconfirmed(t *testing.T) {
	f := newFake(t)
	p, r := testProvider(t, f)
	f.add(r, p.installation, map[string]string{keyBootstrap: "complete"})
	f.exit = func([]string) (string, string, *int) { return "partial", "", nil }
	if _, err := p.RunCommand(bounded(t), r, sandbox.Command{Args: []string{"tool"}}); !errors.Is(err, sandbox.ErrCommandUnconfirmed) {
		t.Fatal(err)
	}
}

func TestRunCommandRequiresBootstrappedRunningSandbox(t *testing.T) {
	f := newFake(t)
	p, r := testProvider(t, f)
	f.add(r, p.installation, nil)
	if _, err := p.RunCommand(bounded(t), r, sandbox.Command{Args: []string{"tool"}}); !errors.Is(err, sandbox.ErrInvalid) || len(f.commands) != 0 {
		t.Fatal(err)
	}
}

func TestObserveMapsMetrics(t *testing.T) {
	f := newFake(t)
	p, r := testProvider(t, f)
	f.add(r, p.installation, map[string]string{keyBootstrap: "complete"})
	now := time.Now()
	f.metrics = map[string]any{"cpu_count": 2, "cpu_used_pct": 25.0, "mem_total_mib": 4096, "mem_used_mib": 512, "timestamp": now.Add(5 * time.Second).UnixMilli()}
	target := runtimeobs.Target{TenantID: r.TenantID, EnvironmentID: r.EnvironmentID, Mode: runtimeobs.ModeManaged, Instance: runtimeobs.Instance{AllocationID: r.AllocationID, ProviderKey: p.installation}}
	got, err := p.Observe(bounded(t), target)
	if err != nil || *got.CPUCapacityCores != 2 || *got.CPUUtilizationRatio != 0.25 || *got.MemoryLimitBytes != 4096<<20 || *got.MemoryUsageBytes != 512<<20 ||
		got.ObservedAt.After(time.Now()) || got.StartedAt == nil || got.CPUUsageSecondsTotal != nil {
		t.Fatalf("sample = %+v, %v", got, err)
	}
	f.metrics["timestamp"] = now.Add(time.Hour).UnixMilli()
	if _, err := p.Observe(bounded(t), target); !errors.Is(err, runtimeobs.ErrUnavailable) {
		t.Fatal("future sample accepted", err)
	}
	if err := p.Kill(bounded(t), r); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Observe(bounded(t), target); !errors.Is(err, runtimeobs.ErrNotRunning) {
		t.Fatal("missing sandbox:", err)
	}
	for _, calls := range f.Calls() {
		if calls == "patch" || calls == "create" || calls == "renew" {
			t.Fatal("observe wrote", calls)
		}
	}
}
