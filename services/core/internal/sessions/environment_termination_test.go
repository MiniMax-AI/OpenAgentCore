package sessions

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

func TestEnvironmentType(t *testing.T) {
	for configuration, want := range map[string]string{
		`{"type":"self_hosted"}`:   "self_hosted",
		`{"type":"openai_hosted"}`: "openai_hosted",
		`{"type":"cloud"}`:         "",
		`{}`:                       "",
		`[`:                        "",
	} {
		got, err := EnvironmentType(json.RawMessage(configuration))
		if got != want || (err == nil) != (want != "") {
			t.Errorf("%s: %q, %v", configuration, got, err)
		}
	}
}

func TestEnvironmentStateChange(t *testing.T) {
	connected := EnvironmentStateChange("environment", "self_hosted", "connected")
	if connected.Event.Type != "agent.session.environment.connected" || !reflect.DeepEqual(connected.Event.Environment, &v1.SessionEnvironmentState{ID: "environment", Type: "self_hosted", Status: "connected"}) {
		t.Fatalf("connected %+v", connected.Event)
	}
	failed := EnvironmentStateChange("environment", "openai_hosted", "failed")
	if failed.Event.Type != "agent.session.environment.failed" || failed.Event.Environment.Error == nil || failed.Event.Environment.Error.Code != "environment_connection_failed" {
		t.Fatalf("failed %+v", failed.Event)
	}
}

func TestDecideEnvironmentTermination(t *testing.T) {
	for _, test := range []struct {
		status  string
		expired bool
		want    environmentTermination
	}{
		{"connected", false, terminationFails},
		{"disconnected", true, terminationExpires},
		{"failed", false, terminationSettles},
		{"failed", true, terminationSettles},
		{"expired", true, terminationSettles},
	} {
		if got := decideEnvironmentTermination(test.status, test.expired); got != test.want {
			t.Errorf("%s expired=%v: %v", test.status, test.expired, got)
		}
	}
}

// FailEnvironment journals the Environment failure, then cancels the Session's
// work, then reports the error and the failed Session with the recorded
// failure time and the settled input activity.
func TestFailEnvironment(t *testing.T) {
	running := turnWith(TurnInProgress)
	failedAt := time.Unix(1700000000, 0)
	settled := &EnvironmentInputState{State: EnvironmentInputFailed, EnvironmentType: "openai_hosted", SettledAt: failedAt}
	usage := json.RawMessage(`{"input_tokens":1}`)
	environment := Environment{ID: "environment", Configuration: json.RawMessage(`{"type":"openai_hosted"}`)}
	var changes []SessionChange
	f := &fakeTx{t: t,
		recordEnvironmentFailure: returns(failedAt), appendChanges: collect(&changes), failPendingInput: done,
		loadActiveTurn: activeTurn(&running), requestTurnCancel: done, cancelPendingInput: done,
		loadEnvironmentInput: inputs(settled), loadUsage: returns(usage),
	}
	if err := FailEnvironment(t.Context(), f, environment, "Failed to provision environment", nil); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f,
		"RecordEnvironmentFailure environment Failed to provision environment",
		"AppendChanges agent.session.environment.failed",
		"FailPendingInput",
		"LoadActiveTurn", "RequestTurnCancel "+testTurn, "CancelPendingInput",
		"LoadEnvironmentInput", "LoadUsage",
		"AppendChanges error,agent.session.failed",
	)
	activity, _ := InputActivity(settled)
	want := append([]SessionChange{EnvironmentStateChange("environment", "openai_hosted", "failed")},
		environmentFailedChanges(EnvironmentFailure{Reason: "Failed to provision environment", FailedAt: failedAt}, activity, false, usage)...)
	if !reflect.DeepEqual(changes, want) || changes[1].Event.Error.Message != "Failed to provision environment" || !changes[2].Settled || changes[2].EnvironmentFailure.FailedAt != failedAt {
		t.Fatalf("changes %+v", changes)
	}

	invalid := &fakeTx{t: t, recordEnvironmentFailure: returns(failedAt)}
	if err := FailEnvironment(t.Context(), invalid, Environment{ID: "environment", Configuration: json.RawMessage(`{}`)}, "reason", nil); err == nil {
		t.Fatal("invalid Environment type failed")
	}
	assertCalls(t, invalid, "RecordEnvironmentFailure environment reason")
}

func TestTerminateEnvironment(t *testing.T) {
	configuration := json.RawMessage(`{"type":"openai_hosted"}`)
	connected := Environment{ID: "environment", Status: "connected", Configuration: configuration}
	var changes []SessionChange
	live := &fakeTx{t: t,
		loadEnvironment: returns(connected), recordEnvironmentFailure: returns(time.Unix(1700000000, 0)), appendChanges: collect(&changes),
		failPendingInput: done, loadActiveTurn: activeTurn(nil), cancelPendingInput: done,
		loadEnvironmentInput: inputs(), loadUsage: returns(json.RawMessage(`{}`)),
	}
	if err := TerminateEnvironment(t.Context(), live, false, "reason", nil); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, live,
		"LoadEnvironment", "RecordEnvironmentFailure environment reason",
		"AppendChanges agent.session.environment.failed",
		"FailPendingInput", "LoadActiveTurn", "CancelPendingInput",
		"LoadEnvironmentInput", "LoadUsage",
		"AppendChanges error,agent.session.failed",
	)

	expired := &fakeTx{t: t,
		loadEnvironment: returns(connected), loadEnvironmentInput: inputs(), expireEnvironment: done,
		failPendingInput: done, loadActiveTurn: activeTurn(nil), cancelPendingInput: done,
	}
	if err := TerminateEnvironment(t.Context(), expired, true, "reason", nil); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, expired, "LoadEnvironment", "LoadEnvironmentInput", "ExpireEnvironment environment", "FailPendingInput", "LoadActiveTurn", "CancelPendingInput", "LoadEnvironmentInput")

	terminal := &fakeTx{t: t,
		loadEnvironment:      returns(Environment{ID: "environment", Status: "failed", Configuration: configuration}),
		loadEnvironmentInput: inputs(), failPendingInput: done, loadActiveTurn: activeTurn(nil), cancelPendingInput: done,
	}
	if err := TerminateEnvironment(t.Context(), terminal, false, "reason", nil); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, terminal, "LoadEnvironment", "LoadEnvironmentInput", "FailPendingInput", "LoadActiveTurn", "CancelPendingInput", "LoadEnvironmentInput")
}
