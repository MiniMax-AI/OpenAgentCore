package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestInputActivity(t *testing.T) {
	created, settled := time.Unix(1700000000, 0), time.Unix(1700000060, 0)
	state := func(state, kind, status string, initial bool) *EnvironmentInputState {
		s := &EnvironmentInputState{State: state, Initial: initial, CreatedAt: created, EnvironmentID: "environment", EnvironmentType: kind, EnvironmentStatus: status}
		if state != EnvironmentInputPending {
			s.SettledAt = settled
		}
		return s
	}
	coded := state(EnvironmentInputFailed, "self_hosted", "connected", false)
	coded.FailureCode = "model_provider_required"
	for name, test := range map[string]struct {
		state    *EnvironmentInputState
		activity *EnvironmentInputActivity
		pending  bool
	}{
		"none":                          {nil, nil, false},
		"pending disconnected":          {state(EnvironmentInputPending, "self_hosted", "disconnected", false), &EnvironmentInputActivity{Status: "requires_action", EnvironmentID: "environment", LastActiveAt: created}, true},
		"pending connected":             {state(EnvironmentInputPending, "self_hosted", "connected", false), &EnvironmentInputActivity{Status: "idle", LastActiveAt: created}, true},
		"pending hosted":                {state(EnvironmentInputPending, "openai_hosted", "disconnected", false), &EnvironmentInputActivity{Status: "idle", LastActiveAt: created}, true},
		"failed":                        {state(EnvironmentInputFailed, "self_hosted", "connected", false), &EnvironmentInputActivity{Status: "failed", Failure: "environment_unavailable", LastActiveAt: settled}, false},
		"failed with code":              {coded, &EnvironmentInputActivity{Status: "failed", Failure: "model_provider_required", LastActiveAt: settled}, false},
		"initial expired":               {state(EnvironmentInputExpired, "self_hosted", "disconnected", true), &EnvironmentInputActivity{Status: "failed", LastActiveAt: settled}, false},
		"expired":                       {state(EnvironmentInputExpired, "self_hosted", "disconnected", false), &EnvironmentInputActivity{Status: "idle", LastActiveAt: settled}, false},
		"hosted initial pending":        {state(EnvironmentInputPending, "openai_hosted", "disconnected", true), nil, true},
		"hosted initial cancelled":      {state(EnvironmentInputCancelled, "openai_hosted", "disconnected", true), nil, false},
		"hosted initial failed reports": {state(EnvironmentInputFailed, "openai_hosted", "disconnected", true), &EnvironmentInputActivity{Status: "failed", Failure: "environment_unavailable", LastActiveAt: settled}, false},
	} {
		activity, pending := InputActivity(test.state)
		if !reflect.DeepEqual(activity, test.activity) || pending != test.pending {
			t.Errorf("%s: %+v, %v", name, activity, pending)
		}
	}
}

func TestInputActivityChanged(t *testing.T) {
	idle := &EnvironmentInputActivity{Status: "idle"}
	for name, test := range map[string]struct {
		before, after *EnvironmentInputActivity
		want          bool
	}{
		"admitted":    {idle, nil, false},
		"new":         {nil, idle, true},
		"same":        {idle, &EnvironmentInputActivity{Status: "idle", LastActiveAt: time.Unix(1, 0)}, false},
		"status":      {idle, &EnvironmentInputActivity{Status: "failed"}, true},
		"environment": {&EnvironmentInputActivity{Status: "requires_action", EnvironmentID: "a"}, &EnvironmentInputActivity{Status: "requires_action", EnvironmentID: "b"}, true},
		"failure":     {&EnvironmentInputActivity{Status: "failed", Failure: "a"}, &EnvironmentInputActivity{Status: "failed", Failure: "b"}, true},
	} {
		if got := inputActivityChanged(test.before, test.after); got != test.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

// TrackInputActivity reads the Session's usage and journals a snapshot only
// when apply changed the input activity.
func TestTrackInputActivity(t *testing.T) {
	pending := &EnvironmentInputState{State: EnvironmentInputPending, EnvironmentID: "environment", EnvironmentType: "self_hosted", EnvironmentStatus: "disconnected"}
	failed := &EnvironmentInputState{State: EnvironmentInputFailed, EnvironmentType: "self_hosted", SettledAt: time.Unix(1700000000, 0)}
	usage := json.RawMessage(`{"input_tokens":1}`)
	apply := func(f *fakeTx, err error) func(context.Context) error {
		return func(context.Context) error {
			f.calls = append(f.calls, "apply")
			return err
		}
	}

	var changes []SessionChange
	changed := &fakeTx{t: t, loadEnvironmentInput: inputs(pending, failed), loadUsage: returns(usage), appendChanges: collect(&changes)}
	if err := TrackInputActivity(t.Context(), changed, apply(changed, nil)); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, changed, "LoadEnvironmentInput", "apply", "LoadEnvironmentInput", "LoadUsage", "AppendChanges agent.session.failed")
	after, _ := InputActivity(failed)
	if !reflect.DeepEqual(changes, []SessionChange{inputActivityChange(after, false, usage)}) || !changes[0].Settled {
		t.Fatalf("changes %+v", changes)
	}

	for name, states := range map[string][]*EnvironmentInputState{"unchanged": {pending, pending}, "admitted": {pending, nil}} {
		f := &fakeTx{t: t, loadEnvironmentInput: inputs(states...)}
		if err := TrackInputActivity(t.Context(), f, apply(f, nil)); err != nil {
			t.Fatal(name, err)
		}
		assertCalls(t, f, "LoadEnvironmentInput", "apply", "LoadEnvironmentInput")
	}

	failing := &fakeTx{t: t, loadEnvironmentInput: inputs(pending, failed)}
	if err := TrackInputActivity(t.Context(), failing, apply(failing, errStorage)); !errors.Is(err, errStorage) {
		t.Fatal(err)
	}
	assertCalls(t, failing, "LoadEnvironmentInput", "apply")
}
