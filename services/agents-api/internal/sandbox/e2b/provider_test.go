package e2b

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
)

type fakeCaller struct {
	response Response
	err      error
	requests []Request
}

func (f *fakeCaller) Call(_ context.Context, q Request) (Response, error) {
	f.requests = append(f.requests, q)
	return f.response, f.err
}
func fixture(t *testing.T) (*Provider, *fakeCaller, sandbox.Reference) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "helper")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config := Config{Binary: binary, StateDir: root, InstallationID: uuid.NewString(), APIKey: "private-test-key", Template: "test:" + uuid.NewString(), TimeoutSeconds: 300}
	caller := &fakeCaller{response: Response{Version: ProtocolVersion}}
	provider, err := NewWithCaller(config, caller)
	if err != nil {
		t.Fatal(err)
	}
	return provider, caller, sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}
}
func bounded(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	t.Cleanup(cancel)
	return ctx
}
func TestReadOnlyConstructionAndValidation(t *testing.T) {
	p, f, _ := fixture(t)
	if len(f.requests) != 0 {
		t.Fatal("construction invoked helper")
	}
	for _, change := range []func(*Config){func(c *Config) { c.Template = "mutable" }, func(c *Config) { c.APIKey = "key\n" }, func(c *Config) { c.StateDir = "relative" }, func(c *Config) { c.TimeoutSeconds = 0 }, func(c *Config) { c.Binary = "/missing/helper" }} {
		c := p.config
		change(&c)
		if _, err := NewWithCaller(c, f); !errors.Is(err, sandbox.ErrInvalid) {
			t.Fatal("invalid configuration accepted")
		}
	}
}
func TestReferenceBoundSettlement(t *testing.T) {
	p, f, r := fixture(t)
	f.response.Info = &sandbox.Info{Reference: r, State: "absent", CreateSettled: true}
	info, err := p.GetInfo(bounded(t), r)
	if err != nil || !info.CreateSettled || info.State != "absent" {
		t.Fatalf("absence proof: %+v %v", info, err)
	}
	f.response.Info.AllocationID = uuid.NewString()
	if _, err = p.GetInfo(bounded(t), r); !errors.Is(err, sandbox.ErrComputeUnconfirmed) {
		t.Fatal("foreign proof accepted", err)
	}
}
func TestUnknownDoesNotLeakHelperDiagnosticOrProveAbsence(t *testing.T) {
	p, f, r := fixture(t)
	f.err = errors.New("private-account-key in SDK response")
	info, err := p.GetInfo(bounded(t), r)
	if !errors.Is(err, sandbox.ErrComputeUnconfirmed) || info.CreateSettled {
		t.Fatal(info, err)
	}
	if err.Error() != "sandbox lifecycle outcome unconfirmed" {
		t.Fatal("helper diagnostic leaked")
	}
	f.err = nil
	f.response.ErrorCode = "not_found"
	if _, err = p.GetInfo(bounded(t), r); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatal(err)
	}
}
func TestKillRequiresTerminalProof(t *testing.T) {
	p, f, r := fixture(t)
	for _, info := range []*sandbox.Info{nil, {Reference: r, State: "absent"}, {Reference: r, State: "running", CreateSettled: true}} {
		f.response.Info = info
		if !errors.Is(p.Kill(bounded(t), r), sandbox.ErrComputeUnconfirmed) {
			t.Fatal("unproven cleanup accepted")
		}
	}
	f.response.Info = &sandbox.Info{Reference: r, State: "absent", CreateSettled: true}
	if err := p.Kill(bounded(t), r); err != nil {
		t.Fatal(err)
	}
}
func TestCreateAndCommandUseOnlyPrivateRequest(t *testing.T) {
	p, f, r := fixture(t)
	b := sandbox.Bootstrap{Reference: r, SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "https://core.example/api/v1", Credential: "private-bootstrap", NetworkAccess: "enabled"}
	f.response.Info = &sandbox.Info{Reference: r, State: "running", ProviderID: "native-id", CreateSettled: true, BootstrapComplete: true}
	if _, err := p.Create(bounded(t), b); err != nil {
		t.Fatal(err)
	}
	q := f.requests[0]
	if q.Operation != "create" || q.Bootstrap == nil || q.Bootstrap.Credential != b.Credential || q.Config.APIKey != p.config.APIKey {
		t.Fatal("private request lost")
	}
	f.response.Info = nil
	f.response.Command = &sandbox.CommandResult{ExitCode: 7, Stdout: "output"}
	got, err := p.RunCommand(bounded(t), r, sandbox.Command{Args: []string{"cat"}, Stdin: []byte("private input")})
	if err != nil || got.ExitCode != 7 {
		t.Fatal(got, err)
	}
	f.err = context.DeadlineExceeded
	if _, err = p.RunCommand(bounded(t), r, sandbox.Command{Args: []string{"true"}}); !errors.Is(err, sandbox.ErrCommandUnconfirmed) {
		t.Fatal(err)
	}
	if len(f.requests) != 3 {
		t.Fatal("operation was replayed")
	}
}
func TestDeadlineAndCommandAdmission(t *testing.T) {
	p, f, r := fixture(t)
	if _, err := p.GetInfo(context.Background(), r); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal(err)
	}
	for _, cmd := range []sandbox.Command{{}, {Args: []string{"x\x00"}}, {Args: []string{"cat"}, Directory: "relative"}} {
		if _, err := p.RunCommand(bounded(t), r, cmd); !errors.Is(err, sandbox.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if len(f.requests) != 0 {
		t.Fatal("invalid operation reached helper")
	}
}

func TestDefinitePreHelperCreateFailureCarriesAbsenceProof(t *testing.T) {
	p, f, r := fixture(t)
	b := sandbox.Bootstrap{Reference: r, SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "https://core.example/api/v1", Credential: "private", NetworkAccess: "enabled"}
	for _, err := range []error{errHelperNotStarted, context.DeadlineExceeded} {
		f.err = err
		info, gotErr := p.Create(bounded(t), b)
		if gotErr == nil {
			t.Fatal("failure lost")
		}
		if errors.Is(err, errHelperNotStarted) {
			if !info.CreateSettled || info.State != "absent" {
				t.Fatal("missing definite absence proof")
			}
		} else if info.CreateSettled {
			t.Fatal("timeout proved absence")
		}
	}
}
