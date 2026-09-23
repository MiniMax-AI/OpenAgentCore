package execution

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

const setupCanary = "CANARY-runtime-setup-9b3e"

// receiptProvider answers every initialization command with one fixed result.
type receiptProvider struct {
	sandbox.Provider
	result sandbox.CommandResult
	err    error
}

func (p receiptProvider) RunCommand(context.Context, sandbox.Reference, sandbox.Command) (sandbox.CommandResult, error) {
	return p.result, p.err
}

// Only a process status 1, empty stderr and a version-1 failed receipt confirm a
// failed step, and only an integer exit_code from 1 to 255 is taken from it.
// Other receipt fields are ignored, never read; everything else stays generic.
func TestRuntimeSetupReceiptOutcomes(t *testing.T) {
	setup := runtimeSetupOperation{Version: 1, Action: "setup", Network: "enabled", Command: "echo " + setupCanary, CWD: "/workspace", Index: 2}
	plugin := runtimeSetupOperation{Capabilities: &agentcapabilities.Operation{Version: 1, Action: "plugin"}}
	unconfirmed := -1
	for _, test := range []struct {
		name      string
		operation runtimeSetupOperation
		result    sandbox.CommandResult
		err       error
		exitCode  int // -1 unconfirmed, 0 confirmed without a status
	}{
		{"completed", setup, sandbox.CommandResult{Stdout: `{"version":1,"outcome":"completed"}`}, nil, -2},
		{"exit status", setup, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed","exit_code":3}`}, nil, 3},
		{"highest exit status", setup, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed","exit_code":255}` + "\n"}, nil, 255},
		{"old receipt", setup, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed"}`}, nil, 0},
		{"zero exit status", setup, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed","exit_code":0}`}, nil, 0},
		{"exit status above 255", setup, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed","exit_code":256}`}, nil, 0},
		{"negative exit status", setup, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed","exit_code":-9}`}, nil, 0},
		{"null exit status", setup, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed","exit_code":null}`}, nil, 0},
		// Lenient for older and newer images: other fields are never read.
		{"ignored output field", setup, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed","exit_code":3,"output":"` + setupCanary + `"}`}, nil, 3},
		{"duplicate key keeps the last", setup, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed","exit_code":3,"exit_code":4}`}, nil, 4},
		{"string exit status", setup, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed","exit_code":"3"}`}, nil, unconfirmed},
		{"fractional exit status", setup, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed","exit_code":3.5}`}, nil, unconfirmed},
		{"stderr", setup, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed","exit_code":3}`, Stderr: setupCanary}, nil, unconfirmed},
		{"process status 2", setup, sandbox.CommandResult{ExitCode: 2, Stdout: `{"version":1,"outcome":"failed","exit_code":3}`}, nil, unconfirmed},
		{"process status 0", setup, sandbox.CommandResult{Stdout: `{"version":1,"outcome":"failed","exit_code":3}`}, nil, unconfirmed},
		{"completed with status 1", setup, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"completed"}`}, nil, unconfirmed},
		{"version 2", setup, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":2,"outcome":"failed","exit_code":3}`}, nil, unconfirmed},
		{"raw output", setup, sandbox.CommandResult{ExitCode: 3, Stdout: setupCanary}, nil, unconfirmed},
		{"trailing output", setup, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed","exit_code":3}` + setupCanary}, nil, unconfirmed},
		{"provider error", setup, sandbox.CommandResult{}, sandbox.ErrCommandUnconfirmed, unconfirmed},
		{"plugin step", plugin, sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed","exit_code":3}`}, nil, unconfirmed},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := runRuntimeSetup(t.Context(), receiptProvider{result: test.result, err: test.err}, sandbox.Reference{}, test.operation)
			var failed *runtimeStepFailure
			switch {
			case test.exitCode == -2:
				if err != nil {
					t.Fatal(err)
				}
			case test.exitCode == unconfirmed:
				if err == nil || errors.As(err, &failed) {
					t.Fatal("unconfirmed result became a confirmed failure", err)
				}
			default:
				if !errors.As(err, &failed) || failed.exitCode != test.exitCode {
					t.Fatal("confirmed failure", err, failed)
				}
			}
			if err != nil && strings.Contains(err.Error(), setupCanary) {
				t.Fatal("receipt or output reached the error", err)
			}
		})
	}
}

func TestRuntimeSetupFailureLabels(t *testing.T) {
	for operation, want := range map[string]store.ProvisioningFailure{
		"setup":     {Step: store.ProvisioningSetupCommand, Index: 2, ExitCode: 3},
		"python":    {Step: store.ProvisioningPythonPackages, Index: 2, ExitCode: 3},
		"npm":       {Step: store.ProvisioningNPMPackages, Index: 2, ExitCode: 3},
		"system":    {Step: store.ProvisioningSystemPackages, Index: 2, ExitCode: 3},
		"skill":     {Step: store.ProvisioningSkill, Index: 2, ExitCode: 3},
		"configure": {},
		"":          {},
	} {
		if got := (runtimeSetupOperation{Action: operation, Index: 2}).provisioningFailure(3); got != want {
			t.Errorf("%q: %+v", operation, got)
		}
	}
	// setupOperations numbers setup commands from zero.
	operations := setupOperations(store.EnvironmentSetup{Commands: []store.SetupCommand{{Command: "a"}, {Command: "b"}}})
	if len(operations) != 3 || operations[1].Index != 0 || operations[2].Index != 1 || operations[2].Action != "setup" {
		t.Fatal("setup command positions", operations)
	}
}

// The file writer exits 0 with a failed receipt when it committed nothing; an
// unknown outcome, stderr or a wrong size stays unconfirmed.
func TestInitialFileReceiptOutcomes(t *testing.T) {
	body := []byte(setupCanary)
	file := store.InitialFileMetadata{Path: "/workspace/a"}
	size := int64(len(body))
	file.SizeBytes = &size
	for _, test := range []struct {
		name      string
		result    sandbox.CommandResult
		confirmed bool
	}{
		{"failed", sandbox.CommandResult{Stdout: `{"version":1,"outcome":"failed","error":"write_failed"}`}, true},
		{"unknown", sandbox.CommandResult{Stdout: `{"version":1,"outcome":"unknown","error":"write_failed"}`}, false},
		{"stderr", sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed"}`, Stderr: setupCanary}, false},
		{"wrong size", sandbox.CommandResult{Stdout: `{"version":1,"outcome":"completed","size_bytes":1}`}, false},
		{"raw output", sandbox.CommandResult{ExitCode: 2, Stdout: setupCanary}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := installInitialFile(t.Context(), receiptProvider{result: test.result}, sandbox.Reference{}, file, body)
			var failed *runtimeStepFailure
			if err == nil || errors.As(err, &failed) != test.confirmed || strings.Contains(err.Error(), setupCanary) {
				t.Fatal(err)
			}
		})
	}
}
