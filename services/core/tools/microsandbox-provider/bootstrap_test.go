//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/contracttest"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
)

type captureCaller func(wire.Request)

func (f captureCaller) Call(_ context.Context, q wire.Request) (wire.Response, error) {
	f(q)
	return wire.Response{}, errors.New("captured")
}

// Core's adapter sends SandboxIO to the helper, and the helper hands the
// guest both launch inputs on stdin.
func TestCreateDeliversBothLaunchInputs(t *testing.T) {
	config := wire.Config{
		InstallationID: "11111111-1111-4111-8111-111111111111", HelperPath: "/helper", RuntimeHome: "/private/msb", RuntimePath: "/private/bin/msb", FirmwarePath: "/private/lib/libkrunfw.so",
		RuntimeSHA256: strings.Repeat("a", 64), FirmwareSHA256: strings.Repeat("b", 64), Image: "registry/runtime@sha256:" + strings.Repeat("c", 64),
		MemoryMiB: 2048, CPUs: 2, RootDiskMiB: 4096, EnvironmentDiskMiB: 2048, Network: wire.NetworkPolicy{DefaultEgress: "deny", DefaultIngress: "deny"},
	}
	b := contracttest.Bootstrap(sandbox.Reference{TenantID: "22222222-2222-4222-8222-222222222222", EnvironmentID: "33333333-3333-4333-8333-333333333333", AllocationID: "44444444-4444-4444-8444-444444444444"})
	var sent []byte
	p, err := wire.NewWithCaller(config, captureCaller(func(q wire.Request) { sent, _ = json.Marshal(q) }))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, _ = p.Create(ctx, b)
	// Decode as serve does.
	decoder := json.NewDecoder(bytes.NewReader(sent))
	decoder.DisallowUnknownFields()
	var q wire.Request
	if err := decoder.Decode(&q); err != nil || wire.ValidateRequest(q) != nil || q.Bootstrap == nil || q.Bootstrap.SandboxIO != b.SandboxIO {
		t.Fatal("the helper request lost the Sandbox I/O input", err)
	}
	payload, err := launchInputs(*q.Bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	runtime, _ := b.RuntimeConnection().Marshal()
	serveInput, _ := b.SandboxIO.Marshal()
	var got map[string]json.RawMessage
	if err := json.Unmarshal(payload, &got); err != nil || len(got) != 2 || !bytes.Equal(got["Runtime"], runtime) || !bytes.Equal(got["SandboxIO"], serveInput) {
		t.Fatalf("stdin payload has %v", err)
	}
	if !strings.Contains(bootstrapScript, "b['Runtime']") || !strings.Contains(bootstrapScript, "b['SandboxIO']") {
		t.Fatal("the bootstrap script does not read both launch inputs")
	}
}
