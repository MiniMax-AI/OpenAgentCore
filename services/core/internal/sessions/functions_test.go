package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeFunctionTx is fakeTx with the function-call methods, equally strict: it
// shares fakeTx's call log, and a method whose func is unset fails the test.
type fakeFunctionTx struct {
	*fakeTx

	loadPendingFunctionCalls func() ([]FunctionCall, error)
	findResultTurn           func() (Turn, bool, error)
	matchFunctionResult      func() (FunctionResultMatch, error)
	submitFunctionResult     func() error
	hasFunctionCall          func() (bool, error)
	matchFunctionCall        func() (FunctionCallMatch, error)
	createFunctionCall       func() error
	loadFunctionCall         func() (FunctionCall, bool, error)
	applyFunctionResult      func() error
	applyFunctionTurnStatus  func() (Turn, error)
}

var (
	_ FunctionResultTx = (*fakeFunctionTx)(nil)
	_ FunctionTx       = (*fakeFunctionTx)(nil)
)

func (f *fakeFunctionTx) LoadPendingFunctionCalls(_ context.Context, turn string) ([]FunctionCall, error) {
	f.record("LoadPendingFunctionCalls", f.loadPendingFunctionCalls != nil, turn)
	return f.loadPendingFunctionCalls()
}

func (f *fakeFunctionTx) FindResultTurn(_ context.Context, turn string) (Turn, bool, error) {
	f.record("FindResultTurn", f.findResultTurn != nil, turn)
	return f.findResultTurn()
}

func (f *fakeFunctionTx) MatchFunctionResult(_ context.Context, turn, call string, result json.RawMessage) (FunctionResultMatch, error) {
	f.record("MatchFunctionResult", f.matchFunctionResult != nil, turn, call, string(result))
	return f.matchFunctionResult()
}

func (f *fakeFunctionTx) SubmitFunctionResult(_ context.Context, turn, call string, result json.RawMessage) error {
	f.record("SubmitFunctionResult", f.submitFunctionResult != nil, turn, call, string(result))
	return f.submitFunctionResult()
}

func (f *fakeFunctionTx) HasFunctionCall(_ context.Context, call string) (bool, error) {
	f.record("HasFunctionCall", f.hasFunctionCall != nil, call)
	return f.hasFunctionCall()
}

func (f *fakeFunctionTx) MatchFunctionCall(_ context.Context, turn string, call FunctionCall) (FunctionCallMatch, error) {
	f.record("MatchFunctionCall", f.matchFunctionCall != nil, turn, call.CallID)
	return f.matchFunctionCall()
}

func (f *fakeFunctionTx) CreateFunctionCall(_ context.Context, turn string, call FunctionCall) error {
	f.record("CreateFunctionCall", f.createFunctionCall != nil, turn, call.CallID)
	return f.createFunctionCall()
}

func (f *fakeFunctionTx) LoadFunctionCall(_ context.Context, turn, call string) (FunctionCall, bool, error) {
	f.record("LoadFunctionCall", f.loadFunctionCall != nil, turn, call)
	return f.loadFunctionCall()
}

func (f *fakeFunctionTx) ApplyFunctionResult(_ context.Context, turn, call string) error {
	f.record("ApplyFunctionResult", f.applyFunctionResult != nil, turn, call)
	return f.applyFunctionResult()
}

func (f *fakeFunctionTx) ApplyFunctionTurnStatus(_ context.Context, turn, expected, status string) (Turn, error) {
	f.record("ApplyFunctionTurnStatus", f.applyFunctionTurnStatus != nil, turn, expected, status)
	return f.applyFunctionTurnStatus()
}

func cancelling(status string) Turn {
	turn := turnWith(status)
	turn.CancelRequestedAt = time.Unix(1700000001, 0)
	return turn
}

var testCall = FunctionCall{CallID: "call", ExecutorCallID: "native-call", Name: "lookup", Arguments: json.RawMessage(`{"ticket":9007199254740993}`)}

func TestValidateFunctionCall(t *testing.T) {
	for name, change := range map[string]func(*FunctionCall){
		"blank call":        func(c *FunctionCall) { c.CallID = " " },
		"long call":         func(c *FunctionCall) { c.CallID = strings.Repeat("c", 513) },
		"blank executor":    func(c *FunctionCall) { c.ExecutorCallID = "" },
		"blank name":        func(c *FunctionCall) { c.Name = "\t" },
		"long name":         func(c *FunctionCall) { c.Name = strings.Repeat("n", 513) },
		"invalid arguments": func(c *FunctionCall) { c.Arguments = json.RawMessage(`{`) },
		"missing arguments": func(c *FunctionCall) { c.Arguments = nil },
		"oversized": func(c *FunctionCall) {
			c.Arguments = json.RawMessage(`"` + strings.Repeat("a", maxFunctionArguments-1) + `"`)
		},
		"result":             func(c *FunctionCall) { c.Result = json.RawMessage(`{}`) },
		"applied":            func(c *FunctionCall) { c.Applied = true },
		"empty result slice": func(c *FunctionCall) { c.Result = json.RawMessage{} },
	} {
		call := testCall
		change(&call)
		if err := validateFunctionCall(call); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
	bounded := testCall
	bounded.CallID, bounded.Arguments = strings.Repeat("c", 512), json.RawMessage(`"`+strings.Repeat("a", maxFunctionArguments-2)+`"`)
	for _, call := range []FunctionCall{testCall, bounded} {
		if err := validateFunctionCall(call); err != nil {
			t.Error(err)
		}
	}
}

func TestParseFunctionResultInput(t *testing.T) {
	input, err := ParseFunctionResultInput(json.RawMessage(`{"turn_id":"not-a-uuid","call_id":"call","result":{ "success" : true, "output":[{"type":"input_text","text":"a"}] }}`))
	if err != nil || input.TurnID != "not-a-uuid" || input.CallID != "call" || string(input.Result) != `{"output":[{"text":"a","type":"input_text"}],"success":true}` {
		t.Fatalf("%+v %v", input, err)
	}
	for name, raw := range map[string]string{
		"malformed":      `{`,
		"missing turn":   `{"call_id":"call","result":{}}`,
		"blank call":     `{"turn_id":"turn","call_id":" ","result":{}}`,
		"long call":      `{"turn_id":"turn","call_id":"` + strings.Repeat("c", 513) + `","result":{}}`,
		"missing result": `{"turn_id":"turn","call_id":"call"}`,
		"null result":    `{"turn_id":"turn","call_id":"call","result":null}`,
		"array result":   `{"turn_id":"turn","call_id":"call","result":[]}`,
		"string result":  `{"turn_id":"turn","call_id":"call","result":"ok"}`,
	} {
		if _, err := ParseFunctionResultInput(json.RawMessage(raw)); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAcceptsFunctionResults(t *testing.T) {
	for name, test := range map[string]struct {
		turn Turn
		want bool
	}{
		"queued":                 {turnWith(TurnQueued), false},
		"in progress":            {turnWith(TurnInProgress), true},
		"waiting":                {turnWith(TurnWaiting), true},
		"cancelling":             {cancelling(TurnInProgress), false},
		"cancelling and waiting": {cancelling(TurnWaiting), false},
		"completed":              {turnWith(TurnCompleted), false},
		"failed":                 {turnWith(TurnFailed), false},
		"cancelled":              {turnWith(TurnCancelled), false},
	} {
		if got := acceptsFunctionResults(test.turn); got != test.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestDecideFunctionResult(t *testing.T) {
	saved, same := FunctionResultMatch{Recorded: true, Submitted: true}, FunctionResultMatch{Recorded: true, Submitted: true, Matches: true}
	for name, test := range map[string]struct {
		turn  Turn
		match FunctionResultMatch
		store bool
		err   error
	}{
		"new result":                    {turnWith(TurnWaiting), FunctionResultMatch{Recorded: true}, true, nil},
		"identical retry":               {turnWith(TurnWaiting), same, false, nil},
		"identical retry after end":     {turnWith(TurnCompleted), same, false, nil},
		"different result":              {turnWith(TurnWaiting), saved, false, ErrFunctionResultConflict},
		"different result after end":    {turnWith(TurnCancelled), saved, false, ErrFunctionResultConflict},
		"new result while cancelling":   {cancelling(TurnWaiting), FunctionResultMatch{Recorded: true}, false, ErrTurnConflict},
		"new result after the Turn end": {turnWith(TurnFailed), FunctionResultMatch{Recorded: true}, false, ErrTurnConflict},
	} {
		store, err := decideFunctionResult(test.turn, test.match)
		if store != test.store || !errors.Is(err, test.err) || (test.err == nil && err != nil) {
			t.Errorf("%s: %v %v", name, store, err)
		}
	}
}

func TestAdmitFunctionResult(t *testing.T) {
	input := FunctionResultInput{TurnID: testTurn, CallID: "call", Result: json.RawMessage(`{"success":true}`)}
	waiting := turnWith(TurnWaiting)
	find, match, submit := "FindResultTurn "+testTurn, `MatchFunctionResult `+testTurn+` call {"success":true}`, `SubmitFunctionResult `+testTurn+` call {"success":true}`
	t.Run("saves a new result", func(t *testing.T) {
		f := &fakeFunctionTx{fakeTx: &fakeTx{t: t}, findResultTurn: found(waiting), matchFunctionResult: returns(FunctionResultMatch{Recorded: true}), submitFunctionResult: done}
		turn, err := AdmitFunctionResult(t.Context(), f, input)
		if err != nil || !reflect.DeepEqual(turn, waiting) {
			t.Fatal(turn, err)
		}
		assertCalls(t, f.fakeTx, find, match, submit)
	})
	t.Run("identical retry keeps the saved result", func(t *testing.T) {
		completed := turnWith(TurnCompleted)
		f := &fakeFunctionTx{fakeTx: &fakeTx{t: t}, findResultTurn: found(completed), matchFunctionResult: returns(FunctionResultMatch{Recorded: true, Submitted: true, Matches: true})}
		if turn, err := AdmitFunctionResult(t.Context(), f, input); err != nil || turn.ID != testTurn {
			t.Fatal(turn, err)
		}
		assertCalls(t, f.fakeTx, find, match)
	})
	t.Run("decision errors", func(t *testing.T) {
		for name, test := range map[string]struct {
			turn  Turn
			match FunctionResultMatch
			want  error
		}{
			"changed result":        {waiting, FunctionResultMatch{Recorded: true, Submitted: true}, ErrFunctionResultConflict},
			"Turn is cancelling":    {cancelling(TurnWaiting), FunctionResultMatch{Recorded: true}, ErrTurnConflict},
			"Turn already finished": {turnWith(TurnCompleted), FunctionResultMatch{Recorded: true}, ErrTurnConflict},
		} {
			f := &fakeFunctionTx{fakeTx: &fakeTx{t: t}, findResultTurn: found(test.turn), matchFunctionResult: returns(test.match)}
			if _, err := AdmitFunctionResult(t.Context(), f, input); !errors.Is(err, test.want) {
				t.Errorf("%s: %v", name, err)
			}
			assertCalls(t, f.fakeTx, find, match)
		}
	})
	t.Run("classifies a call the Turn does not have", func(t *testing.T) {
		for name, test := range map[string]struct {
			turnFound, elsewhere bool
			want                 error
		}{
			"call of another Turn":         {true, true, ErrFunctionCallTurnMismatch},
			"unknown call":                 {true, false, ErrUnknownFunctionCall},
			"unknown Turn, call elsewhere": {false, true, ErrFunctionCallTurnMismatch},
			"unknown Turn and call":        {false, false, ErrUnknownFunctionCall},
		} {
			f := &fakeFunctionTx{fakeTx: &fakeTx{t: t}, hasFunctionCall: returns(test.elsewhere), matchFunctionResult: returns(FunctionResultMatch{}),
				findResultTurn: func() (Turn, bool, error) { return waiting, test.turnFound, nil }}
			if _, err := AdmitFunctionResult(t.Context(), f, input); !errors.Is(err, test.want) {
				t.Errorf("%s: %v", name, err)
			}
			want := []string{find, match, "HasFunctionCall call"}
			if !test.turnFound {
				want = []string{find, "HasFunctionCall call"}
			}
			assertCalls(t, f.fakeTx, want...)
		}
	})
	t.Run("storage errors", func(t *testing.T) {
		for name, f := range map[string]*fakeFunctionTx{
			"find":   {fakeTx: &fakeTx{t: t}, findResultTurn: func() (Turn, bool, error) { return Turn{}, false, errStorage }},
			"match":  {fakeTx: &fakeTx{t: t}, findResultTurn: found(waiting), matchFunctionResult: func() (FunctionResultMatch, error) { return FunctionResultMatch{}, errStorage }},
			"submit": {fakeTx: &fakeTx{t: t}, findResultTurn: found(waiting), matchFunctionResult: returns(FunctionResultMatch{Recorded: true}), submitFunctionResult: func() error { return errStorage }},
			"classify": {fakeTx: &fakeTx{t: t}, findResultTurn: func() (Turn, bool, error) { return Turn{}, false, nil },
				hasFunctionCall: func() (bool, error) { return false, errStorage }},
		} {
			if _, err := AdmitFunctionResult(t.Context(), f, input); !errors.Is(err, errStorage) {
				t.Errorf("%s: %v", name, err)
			}
		}
	})
}

func TestLoadRequiredActions(t *testing.T) {
	calls := []FunctionCall{testCall, {CallID: "second", ExecutorCallID: "native-second", Name: "search", Arguments: json.RawMessage(`{}`), Result: json.RawMessage(`{"success":true}`)}}
	for _, status := range []string{TurnInProgress, TurnWaiting} {
		f := &fakeFunctionTx{fakeTx: &fakeTx{t: t}, loadPendingFunctionCalls: returns(calls)}
		actions, err := LoadRequiredActions(t.Context(), f, turnWith(status))
		if err != nil || len(actions) != 2 {
			t.Fatal(actions, err)
		}
		assertCalls(t, f.fakeTx, "LoadPendingFunctionCalls "+testTurn)
		for i, action := range actions {
			if action.Type != "function_call" || action.CallID != calls[i].CallID || action.Name != calls[i].Name || action.TurnID != testTurn {
				t.Fatalf("action %d: %+v", i, action)
			}
			// Arguments keep their stored bytes, so large integers keep their precision.
			if raw, ok := action.Arguments.(json.RawMessage); !ok || string(raw) != string(calls[i].Arguments) {
				t.Fatalf("arguments %d: %#v", i, action.Arguments)
			}
		}
	}
	for _, turn := range []Turn{turnWith(TurnQueued), cancelling(TurnWaiting), turnWith(TurnCompleted)} {
		f := &fakeFunctionTx{fakeTx: &fakeTx{t: t}}
		actions, err := LoadRequiredActions(t.Context(), f, turn)
		if err != nil || actions == nil || len(actions) != 0 {
			t.Fatal(turn.Status, actions, err)
		}
		assertCalls(t, f.fakeTx)
	}
	failing := &fakeFunctionTx{fakeTx: &fakeTx{t: t}, loadPendingFunctionCalls: func() ([]FunctionCall, error) { return nil, errStorage }}
	if _, err := LoadRequiredActions(t.Context(), failing, turnWith(TurnWaiting)); !errors.Is(err, errStorage) {
		t.Fatal(err)
	}
}

// found is a fake lookup that finds turn.
func found(turn Turn) func() (Turn, bool, error) {
	return func() (Turn, bool, error) { return turn, true, nil }
}
