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
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

type captureCaller func(wire.Request)

func (f captureCaller) Call(_ context.Context, q wire.Request) (wire.Response, error) {
	f(q)
	return wire.Response{}, errors.New("captured")
}

// Core's adapter sends the Sandbox I/O input to the helper unchanged.
func TestCreateSendsTheSandboxIOInput(t *testing.T) {
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
	if err := decoder.Decode(&q); err != nil || wire.ValidateRequest(q) != nil || q.Bootstrap == nil || *q.Bootstrap != b {
		t.Fatal("the helper request lost the Sandbox I/O input", err)
	}
}

func TestBootstrapRequiresZeroExitAndWholeInput(t *testing.T) {
	for _, tc := range []struct {
		name       string
		events     []sdk.ExecEvent
		inputError error
		confirmed  bool
	}{
		{"confirmed", []sdk.ExecEvent{{Kind: sdk.ExecEventStderr, Data: []byte("noise")}, {Kind: sdk.ExecEventExited}, {Kind: sdk.ExecEventDone}}, nil, true},
		{"nonzero_exit", []sdk.ExecEvent{{Kind: sdk.ExecEventExited, ExitCode: 1}, {Kind: sdk.ExecEventDone}}, nil, false},
		{"missing_exit", []sdk.ExecEvent{{Kind: sdk.ExecEventDone}}, nil, false},
		{"failed_stdin", []sdk.ExecEvent{{Kind: sdk.ExecEventExited}, {Kind: sdk.ExecEventDone}}, errors.New("lost stdin"), false},
		{"duplicate_exit", []sdk.ExecEvent{{Kind: sdk.ExecEventExited}, {Kind: sdk.ExecEventExited}, {Kind: sdk.ExecEventDone}}, nil, false},
		{"stdin_event", []sdk.ExecEvent{{Kind: sdk.ExecEventStdinError}, {Kind: sdk.ExecEventExited}, {Kind: sdk.ExecEventDone}}, nil, false},
		{"stream_lost", []sdk.ExecEvent{{Kind: sdk.ExecEventExited}}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := 0
			recv := func(context.Context) (*sdk.ExecEvent, error) {
				if index >= len(tc.events) {
					return nil, errors.New("stream lost")
				}
				event := tc.events[index]
				index++
				return &event, nil
			}
			input := make(chan error, 1)
			input <- tc.inputError
			err := awaitBootstrap(context.Background(), recv, input)
			if tc.confirmed != (err == nil) || err != nil && !errors.Is(err, sandbox.ErrComputeUnconfirmed) {
				t.Fatalf("bootstrap outcome %v", err)
			}
		})
	}
}
