package microsandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

type callerFunc func(context.Context, Request) (Response, error)

func (f callerFunc) Call(c context.Context, q Request) (Response, error) { return f(c, q) }
func testConfig() Config {
	return Config{
		InstallationID: "11111111-1111-4111-8111-111111111111", HelperPath: "/helper", RuntimeHome: "/private/msb", RuntimePath: "/private/bin/msb", FirmwarePath: "/private/lib/libkrunfw.so",
		RuntimeSHA256: strings.Repeat("a", 64), FirmwareSHA256: strings.Repeat("b", 64), Image: "registry/runtime@sha256:" + strings.Repeat("c", 64),
		MemoryMiB: 2048, CPUs: 2, RootDiskMiB: 4096, EnvironmentDiskMiB: 2048, Network: NetworkPolicy{DefaultEgress: "deny", DefaultIngress: "deny", Rules: []NetworkRule{{Action: "allow", Direction: "egress", Destination: "host"}}},
	}
}
func testRef() sandbox.Reference {
	return sandbox.Reference{TenantID: "22222222-2222-4222-8222-222222222222", EnvironmentID: "33333333-3333-4333-8333-333333333333", AllocationID: "44444444-4444-4444-8444-444444444444"}
}
func testSnapshot() SnapshotIdentity {
	c, r := testConfig(), testRef()
	op := "55555555-5555-4555-8555-555555555555"
	return SnapshotIdentity{Reference: SnapshotReference(c, r, op), ID: "snap_exact", Digest: "digest", CheckpointID: "checkpoint", CheckpointRoot: "root", OperationID: op, SourceName: Name(c, r, 0), SourceID: "local:4"}
}
func deadline(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}
func TestRejectsChangedComputeIdentity(t *testing.T) {
	c, r := testConfig(), testRef()
	p, e := NewWithCaller(c, callerFunc(func(_ context.Context, q Request) (Response, error) {
		got := q.Compute
		got.ID = "local:foreign"
		return Response{Version: ProtocolVersion, State: &State{Compute: got, Status: "running", BootstrapComplete: true}}, nil
	}))
	if e != nil {
		t.Fatal(e)
	}
	_, e = p.GetCompute(deadline(t), r, Compute{Name: Name(c, r, 0), ID: "local:4"})
	if !errors.Is(e, sandbox.ErrOwnership) {
		t.Fatalf("foreign identity accepted: %v", e)
	}
}
func TestRestoreMustRetainExactProvenance(t *testing.T) {
	c, r, s := testConfig(), testRef(), testSnapshot()
	target := Compute{Generation: 1, Name: Name(c, r, 1), RestoredFrom: &s}
	p, _ := NewWithCaller(c, callerFunc(func(_ context.Context, q Request) (Response, error) {
		v := *q.Resume.Target.RestoredFrom
		v.ID = "snap_foreign"
		got := q.Resume.Target
		got.ID = "local:5"
		got.RestoredFrom = &v
		return Response{Version: ProtocolVersion, State: &State{Compute: got, Status: "running", BootstrapComplete: true}}, nil
	}))
	_, e := p.Resume(deadline(t), ResumeRequest{Reference: r, OperationID: "66666666-6666-4666-8666-666666666666", Snapshot: s, Target: target})
	if !errors.Is(e, sandbox.ErrOwnership) {
		t.Fatalf("different snapshot accepted: %v", e)
	}
}
func TestRejectsSnapshotPathBeforeHelper(t *testing.T) {
	c, r, s := testConfig(), testRef(), testSnapshot()
	s.Reference = "/foreign/checkpoint"
	calls := 0
	p, _ := NewWithCaller(c, callerFunc(func(context.Context, Request) (Response, error) { calls++; return Response{}, nil }))
	if e := p.DeleteSnapshot(deadline(t), r, s); !errors.Is(e, sandbox.ErrInvalid) || calls != 0 {
		t.Fatalf("e=%v calls=%d", e, calls)
	}
}
func TestObserveOnlyPreservesCapturedButResidentState(t *testing.T) {
	c, r, s := testConfig(), testRef(), testSnapshot()
	source := Compute{Name: Name(c, r, 0), ID: s.SourceID}
	p, _ := NewWithCaller(c, callerFunc(func(_ context.Context, q Request) (Response, error) {
		if q.Suspend == nil || !q.Suspend.ObserveOnly {
			t.Fatal("observation became mutation")
		}
		return Response{Version: ProtocolVersion, State: &State{Compute: source, Status: "paused", Snapshot: &s}}, nil
	}))
	got, e := p.Suspend(deadline(t), SuspendRequest{Reference: r, OperationID: s.OperationID, Source: source, ObserveOnly: true})
	if e != nil || got.SourceStopped || got.Status != "paused" {
		t.Fatalf("state=%+v error=%v", got, e)
	}
}
func TestCommandUncertaintyAndResultLimits(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response Response
		failure  error
	}{
		{"transport", Response{}, errors.New("lost result")},
		{"missing", Response{Version: ProtocolVersion}, nil},
		{"too_large", Response{Version: ProtocolVersion, Command: &sandbox.CommandResult{Stdout: strings.Repeat("x", MaxOutputBytes+1)}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := NewWithCaller(testConfig(), callerFunc(func(context.Context, Request) (Response, error) { return tc.response, tc.failure }))
			_, e := p.RunCommand(deadline(t), testRef(), sandbox.Command{Args: []string{"/bin/true"}})
			if !errors.Is(e, sandbox.ErrCommandUnconfirmed) {
				t.Fatalf("uncertainty lost: %v", e)
			}
		})
	}
}
func TestNoDeadlineOrForeignAllocationNeverCallsHelper(t *testing.T) {
	calls := 0
	p, _ := NewWithCaller(testConfig(), callerFunc(func(context.Context, Request) (Response, error) { calls++; return Response{}, nil }))
	_, e := p.GetInfo(context.Background(), testRef())
	if !errors.Is(e, sandbox.ErrInvalid) {
		t.Fatal(e)
	}
	r := testRef()
	foreign := r
	foreign.EnvironmentID = "77777777-7777-4777-8777-777777777777"
	initial, initialErr := p.Initial(deadline(t), r)
	if initialErr != nil {
		t.Fatal(initialErr)
	}
	_, e = p.GetCompute(deadline(t), foreign, initial)
	if !errors.Is(e, sandbox.ErrInvalid) || calls != 0 {
		t.Fatalf("e=%v calls=%d", e, calls)
	}
}
func TestHelperEnvironmentDropsCredentialsAndBackendOverrides(t *testing.T) {
	got := strings.Join(HelperEnvironment([]string{"HOME=/home/test", "PATH=/bin", "MSB_BACKEND=cloud", "MSB_CONFIG_PATH=/foreign", "MICROSANDBOX_FFI_PATH=/foreign.so", "OPENAI_API_KEY=secret", "HTTPS_PROXY=secret"}, testConfig()), "\n")
	for _, bad := range []string{"secret", "cloud", "foreign"} {
		if strings.Contains(got, bad) {
			t.Fatalf("ambient override retained: %s", got)
		}
	}
	if !strings.Contains(got, "MSB_BACKEND=local") {
		t.Fatal("missing local backend")
	}
}

func TestMissingSnapshotObservationAllowsOnlyIntactSourceRollback(t *testing.T) {
	for _, status := range []string{"running", "paused", "stopped", "draining", "unknown"} {
		t.Run(status, func(t *testing.T) {
			c, r, s := testConfig(), testRef(), testSnapshot()
			source := Compute{Name: Name(c, r, 0), ID: s.SourceID}
			p, _ := NewWithCaller(c, callerFunc(func(context.Context, Request) (Response, error) {
				return Response{Version: ProtocolVersion, State: &State{Compute: source, Status: status, BootstrapComplete: true}}, nil
			}))
			_, e := p.Suspend(deadline(t), SuspendRequest{Reference: r, OperationID: s.OperationID, Source: source, ObserveOnly: true})
			if status == "running" || status == "paused" {
				if e != nil {
					t.Fatal(e)
				}
			} else if e == nil {
				t.Fatal("unrecoverable source accepted")
			}
		})
	}
}
func TestImageMustUseCompletePinnedOCIManifestDigest(t *testing.T) {
	for _, image := range []string{"image:latest", "image@sha256:garbage", "sha256:" + strings.Repeat("a", 64), "image@sha256:" + strings.Repeat("A", 64)} {
		c := testConfig()
		c.Image = image
		if c.Validate() == nil {
			t.Fatalf("unqualified image admitted: %s", image)
		}
	}
}
