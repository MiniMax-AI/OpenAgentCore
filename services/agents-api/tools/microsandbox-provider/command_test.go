//go:build linux

package main

import (
	"context"
	"errors"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	wire "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
	"strings"
	"testing"
)

func TestCommandRequiresExitAndSuccessfulStdin(t *testing.T) {
	for _, tc := range []struct {
		name       string
		events     []sdk.ExecEvent
		inputError error
		uncertain  bool
	}{
		{"confirmed", []sdk.ExecEvent{{Kind: sdk.ExecEventStdout, Data: []byte("ok")}, {Kind: sdk.ExecEventExited, ExitCode: 7}, {Kind: sdk.ExecEventDone}}, nil, false},
		{"missing_exit", []sdk.ExecEvent{{Kind: sdk.ExecEventDone}}, nil, true},
		{"failed_stdin", []sdk.ExecEvent{{Kind: sdk.ExecEventExited}, {Kind: sdk.ExecEventDone}}, errors.New("lost stdin"), true},
		{"duplicate_exit", []sdk.ExecEvent{{Kind: sdk.ExecEventExited}, {Kind: sdk.ExecEventExited}, {Kind: sdk.ExecEventDone}}, nil, true},
		{"stdin_event", []sdk.ExecEvent{{Kind: sdk.ExecEventStdinError}, {Kind: sdk.ExecEventExited}, {Kind: sdk.ExecEventDone}}, nil, true},
		{"overflow", []sdk.ExecEvent{{Kind: sdk.ExecEventStdout, Data: []byte(strings.Repeat("x", wire.MaxOutputBytes+1))}}, nil, true},
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
			got, e := collectCommand(context.Background(), recv, input)
			if tc.uncertain {
				if !errors.Is(e, sandbox.ErrCommandUnconfirmed) {
					t.Fatalf("outcome confirmed: %+v %v", got, e)
				}
				return
			}
			if e != nil || got.ExitCode != 7 || got.Stdout != "ok" {
				t.Fatalf("nonzero guest exit lost: %+v %v", got, e)
			}
		})
	}
}
