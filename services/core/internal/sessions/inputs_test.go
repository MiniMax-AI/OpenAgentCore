package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/items"
)

// fakeInputTx is fakeFunctionTx with the input methods, equally strict: it
// shares fakeTx's call log, and a method whose func is unset fails the test.
type fakeInputTx struct {
	*fakeFunctionTx

	createTurn             func() (Turn, error)
	createTurnInput        func() (int64, error)
	loadInputBatch         func() ([]InputReceipt, bool, error)
	loadInputGate          func() (InputGate, error)
	findInputReservation   func() (*EnvironmentInputReservation, bool, error)
	loadInputReservation   func() (EnvironmentInputReservation, error)
	createInputReservation func() (EnvironmentInputReservation, error)
	expireInputReservation func() error
	admitInputReservation  func() (time.Time, error)
	failInputReservation   func() error
	recordInputAudit       func() error
}

var _ InputTx = (*fakeInputTx)(nil)

func newInputTx(t *testing.T) *fakeInputTx {
	return &fakeInputTx{fakeFunctionTx: &fakeFunctionTx{fakeTx: &fakeTx{t: t}}}
}

func (f *fakeInputTx) CreateTurn(context.Context) (Turn, error) {
	f.record("CreateTurn", f.createTurn != nil)
	return f.createTurn()
}

func (f *fakeInputTx) CreateTurnInput(_ context.Context, turn, key string, position int32, input Input) (int64, error) {
	f.record("CreateTurnInput", f.createTurnInput != nil, turn, key, fmt.Sprint(position), input.Kind)
	return f.createTurnInput()
}

func (f *fakeInputTx) LoadInputBatch(_ context.Context, key string, batch json.RawMessage) ([]InputReceipt, bool, error) {
	f.record("LoadInputBatch", f.loadInputBatch != nil, key, string(batch))
	return f.loadInputBatch()
}

func (f *fakeInputTx) LoadInputGate(_ context.Context, key string, batch json.RawMessage) (InputGate, error) {
	f.record("LoadInputGate", f.loadInputGate != nil, key, string(batch))
	return f.loadInputGate()
}

func (f *fakeInputTx) FindInputReservation(_ context.Context, key string, batch json.RawMessage) (*EnvironmentInputReservation, bool, error) {
	f.record("FindInputReservation", f.findInputReservation != nil, key, string(batch))
	return f.findInputReservation()
}

func (f *fakeInputTx) LoadInputReservation(_ context.Context, reservation string) (EnvironmentInputReservation, error) {
	f.record("LoadInputReservation", f.loadInputReservation != nil, reservation)
	return f.loadInputReservation()
}

func (f *fakeInputTx) CreateInputReservation(_ context.Context, key string, batch json.RawMessage) (EnvironmentInputReservation, error) {
	f.record("CreateInputReservation", f.createInputReservation != nil, key, string(batch))
	return f.createInputReservation()
}

func (f *fakeInputTx) ExpireInputReservation(_ context.Context, reservation string) error {
	f.record("ExpireInputReservation", f.expireInputReservation != nil, reservation)
	return f.expireInputReservation()
}

func (f *fakeInputTx) AdmitInputReservation(_ context.Context, reservation string) (time.Time, error) {
	f.record("AdmitInputReservation", f.admitInputReservation != nil, reservation)
	return f.admitInputReservation()
}

func (f *fakeInputTx) FailInputReservation(_ context.Context, reservation, code string) error {
	f.record("FailInputReservation", f.failInputReservation != nil, reservation, code)
	return f.failInputReservation()
}

func (f *fakeInputTx) RecordInputAudit(context.Context) error {
	f.record("RecordInputAudit", f.recordInputAudit != nil)
	return f.recordInputAudit()
}

func (s *fakeStorage) WithInputs(ctx context.Context, tenant, session string, apply func(context.Context, InputTx) error) error {
	s.record("WithInputs", s.inputTx != nil, tenant, session)
	return apply(ctx, s.inputTx)
}

// inputKey is the idempotency key of the input batches under test.
const inputKey = "request"

func messageInput(text string) Input {
	payload, _ := json.Marshal(map[string]string{"text": text})
	return Input{Kind: "message", Payload: payload}
}

var cancelInput = Input{Kind: "cancel", Payload: json.RawMessage(`{}`)}

// sequences is a fake CreateTurnInput that allocates sequences from first.
func sequences(first int64) func() (int64, error) {
	return func() (int64, error) {
		first++
		return first - 1, nil
	}
}

// unprojected is a fake LoadInputSource: a message without text, which
// projects no Item.
var unprojected = returns(Source{Turn: testTurn, Kind: "message", Payload: json.RawMessage(`{}`)})

func TestValidateInputs(t *testing.T) {
	batch, encoded, err := ValidateInputs([]Input{{Kind: "message", Payload: json.RawMessage(` {"text":"hi","a":1} `)}, {Kind: "cancel", Payload: json.RawMessage(` { } `)}})
	if err != nil || string(batch[0].Payload) != `{"a":1,"text":"hi"}` || string(encoded) != `[{"kind":"message","payload":{"a":1,"text":"hi"}},{"kind":"cancel","payload":{}}]` {
		t.Fatalf("batch %s, %v", encoded, err)
	}
	for name, inputs := range map[string][]Input{
		"no inputs":            nil,
		"65 inputs":            make([]Input, 65),
		"unsupported kind":     {messageInput("ok"), {Kind: "unsupported", Payload: json.RawMessage(`{}`)}},
		"missing payload":      {{Kind: "message"}},
		"array payload":        {{Kind: "message", Payload: json.RawMessage(`[]`)}},
		"cancel with a target": {{Kind: "cancel", Payload: json.RawMessage(`{"target":"other"}`)}},
		"over 512 KiB":         {messageInput(strings.Repeat("x", 300*1024)), messageInput(strings.Repeat("y", 300*1024))},
	} {
		if _, _, err := ValidateInputs(inputs); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestValidateMessageInputs(t *testing.T) {
	if _, encoded, err := ValidateMessageInputs([]Input{messageInput("hi")}); err != nil || string(encoded) != `[{"kind":"message","payload":{"text":"hi"}}]` {
		t.Fatalf("batch %s, %v", encoded, err)
	}
	for name, inputs := range map[string][]Input{"cancel": {messageInput("hi"), cancelInput}, "no inputs": nil} {
		if _, _, err := ValidateMessageInputs(inputs); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDecideReplay(t *testing.T) {
	for _, test := range []struct {
		used, matches, replay bool
		err                   error
	}{
		{false, false, false, nil},
		{true, true, true, nil},
		{true, false, false, ErrIdempotencyConflict},
	} {
		if replay, err := decideReplay(test.used, test.matches); replay != test.replay || err != test.err {
			t.Errorf("used %v matches %v: %v, %v", test.used, test.matches, replay, err)
		}
	}
}

func TestCheckInputGate(t *testing.T) {
	for gate, want := range map[InputGate]error{
		{Matches: true}:                 nil,
		{Matches: true, Blocked: true}:  ErrInputPending,
		{Matches: false, Blocked: true}: ErrIdempotencyConflict,
	} {
		if err := checkInputGate(gate); err != want {
			t.Errorf("%+v: %v", gate, err)
		}
	}
}

func TestPlaceInput(t *testing.T) {
	for _, test := range []struct {
		kind   string
		active bool
		want   inputPlacement
	}{
		{"message", true, steersTurn},
		{"cancel", true, cancelsTurn},
		{"message", false, startsTurn},
		{"cancel", false, keepsIdentity},
	} {
		if got := placeInput(test.kind, test.active); got != test.want {
			t.Errorf("%s active %v: %v", test.kind, test.active, got)
		}
	}
}

func TestAdmitInput(t *testing.T) {
	running := turnWith(TurnInProgress)
	t.Run("an idle message starts a Turn", func(t *testing.T) {
		var changes []SessionChange
		tx := newInputTx(t)
		tx.loadActiveTurn, tx.createTurn, tx.appendChanges = activeTurn(nil), returns(turnWith(TurnQueued)), collect(&changes)
		tx.createTurnInput, tx.loadUsage = sequences(7), returns(json.RawMessage(`{}`))
		tx.loadInputSource = returns(Source{Turn: testTurn, Kind: "message", Sequence: 7, Payload: json.RawMessage(`{"text":"hi"}`)})
		tx.loadItem, tx.putItem = returns(items.Stored{}), func(items.Change) (*int32, error) { return nil, nil }
		receipt, err := admitInput(t.Context(), tx, inputKey, 0, messageInput("hi"))
		if err != nil || receipt != (InputReceipt{Sequence: 7, TurnID: testTurn}) {
			t.Fatalf("receipt %+v, %v", receipt, err)
		}
		item := items.Identity(testTurn, "input:7")
		assertCalls(t, tx.fakeTx, "LoadActiveTurn", "CreateTurn", "AppendChanges agent.session.turn.created", "CreateTurnInput "+testTurn+" request 0 message",
			"LoadInputSource 7", "LoadItem "+testTurn+" "+item, "PutItem "+testTurn+" "+item, "AppendChanges agent.session.turn.item.added",
			"LoadUsage", "AppendChanges agent.session.in_progress")
	})
	for _, test := range []struct {
		name   string
		input  Input
		active *Turn
		want   InputReceipt
		calls  []string
	}{
		{"a message steers the active Turn", messageInput("more"), &running, InputReceipt{Sequence: 8, TurnID: testTurn},
			[]string{"LoadActiveTurn", "CreateTurnInput " + testTurn + " request 1 message", "LoadInputSource 8"}},
		{"a cancellation requests the active Turn's cancellation", cancelInput, &running, InputReceipt{Sequence: 8, TurnID: testTurn},
			[]string{"LoadActiveTurn", "CreateTurnInput " + testTurn + " request 1 cancel", "RequestTurnCancel " + testTurn, "LoadInputSource 8"}},
		{"an idle cancellation keeps only its retry identity", cancelInput, nil, InputReceipt{Sequence: 8},
			[]string{"LoadActiveTurn", "CreateTurnInput  request 1 cancel", "LoadInputSource 8"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := newInputTx(t)
			tx.loadActiveTurn, tx.createTurnInput, tx.loadInputSource, tx.requestTurnCancel = activeTurn(test.active), sequences(8), unprojected, done
			receipt, err := admitInput(t.Context(), tx, inputKey, 1, test.input)
			if err != nil || receipt != test.want {
				t.Fatalf("receipt %+v, %v", receipt, err)
			}
			assertCalls(t, tx.fakeTx, test.calls...)
		})
	}
	t.Run("a function result joins the Turn of its call", func(t *testing.T) {
		tx := newInputTx(t)
		tx.findResultTurn = func() (Turn, bool, error) { return turnWith(TurnWaiting), true, nil }
		tx.matchFunctionResult, tx.submitFunctionResult, tx.createTurnInput = returns(FunctionResultMatch{Recorded: true}), done, sequences(9)
		payload := json.RawMessage(`{"turn_id":"` + testTurn + `","call_id":"call","result":{"ok":true}}`)
		receipt, err := admitInput(t.Context(), tx, inputKey, 0, Input{Kind: "tool_result", Payload: payload})
		if err != nil || receipt != (InputReceipt{Sequence: 9, TurnID: testTurn}) {
			t.Fatalf("receipt %+v, %v", receipt, err)
		}
		assertCalls(t, tx.fakeTx, "FindResultTurn "+testTurn, "MatchFunctionResult "+testTurn+` call {"ok":true}`, "SubmitFunctionResult "+testTurn+` call {"ok":true}`,
			"CreateTurnInput "+testTurn+" request 0 tool_result")
	})
}

func TestSubmitInputs(t *testing.T) {
	const batch = `[{"kind":"message","payload":{"text":"hi"}},{"kind":"cancel","payload":{}}]`
	inputs := []Input{messageInput("hi"), cancelInput}
	running := turnWith(TurnInProgress)
	submit := func(t *testing.T, tx *fakeInputTx, inputs []Input) ([]InputReceipt, error) {
		storage := &fakeStorage{t: t, inputTx: tx}
		receipts, err := deviceService(t, storage).SubmitInputs(t.Context(), testTenant, testSession, inputKey, inputs)
		assertStorageCalls(t, storage, "WithInputs "+testTenant+" "+testSession)
		return receipts, err
	}
	t.Run("admits the batch in order", func(t *testing.T) {
		tx := newInputTx(t)
		tx.loadInputBatch = func() ([]InputReceipt, bool, error) { return nil, true, nil }
		tx.loadPendingFileWrite, tx.loadInputGate = returns(false), returns(InputGate{Matches: true})
		tx.loadActiveTurn, tx.createTurnInput, tx.loadInputSource, tx.requestTurnCancel = activeTurn(&running), sequences(1), unprojected, done
		tx.recordInputAudit = done
		receipts, err := submit(t, tx, inputs)
		if err != nil || !reflect.DeepEqual(receipts, []InputReceipt{{Sequence: 1, TurnID: testTurn}, {Sequence: 2, TurnID: testTurn}}) {
			t.Fatalf("receipts %+v, %v", receipts, err)
		}
		assertCalls(t, tx.fakeTx, "LoadInputBatch request "+batch, "LoadPendingFileWrite", "LoadInputGate request "+batch,
			"LoadActiveTurn", "CreateTurnInput "+testTurn+" request 0 message", "LoadInputSource 1",
			"LoadActiveTurn", "CreateTurnInput "+testTurn+" request 1 cancel", "RequestTurnCancel "+testTurn, "LoadInputSource 2",
			"RecordInputAudit")
	})
	t.Run("a retry replays its receipts", func(t *testing.T) {
		previous := []InputReceipt{{Sequence: 1, TurnID: testTurn, Replayed: true}, {Sequence: 2, TurnID: testTurn, Replayed: true}}
		tx := newInputTx(t)
		tx.loadInputBatch = func() ([]InputReceipt, bool, error) { return previous, true, nil }
		tx.recordInputAudit = done
		receipts, err := submit(t, tx, inputs)
		if err != nil || !reflect.DeepEqual(receipts, previous) {
			t.Fatalf("receipts %+v, %v", receipts, err)
		}
		assertCalls(t, tx.fakeTx, "LoadInputBatch request "+batch, "RecordInputAudit")
	})
	t.Run("a different batch under the key conflicts", func(t *testing.T) {
		tx := newInputTx(t)
		tx.loadInputBatch = func() ([]InputReceipt, bool, error) { return []InputReceipt{{Sequence: 1}}, false, nil }
		if _, err := submit(t, tx, inputs); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatal(err)
		}
		assertCalls(t, tx.fakeTx, "LoadInputBatch request "+batch)
	})
	t.Run("a pending file write holds a message", func(t *testing.T) {
		tx := newInputTx(t)
		tx.loadInputBatch, tx.loadPendingFileWrite = func() ([]InputReceipt, bool, error) { return nil, true, nil }, returns(true)
		if _, err := submit(t, tx, inputs); !errors.Is(err, ErrTurnConflict) {
			t.Fatal(err)
		}
		assertCalls(t, tx.fakeTx, "LoadInputBatch request "+batch, "LoadPendingFileWrite")
	})
	t.Run("a pending reservation holds a cancellation", func(t *testing.T) {
		tx := newInputTx(t)
		tx.loadInputBatch, tx.loadInputGate = func() ([]InputReceipt, bool, error) { return nil, true, nil }, returns(InputGate{Matches: true, Blocked: true})
		if _, err := submit(t, tx, []Input{cancelInput}); !errors.Is(err, ErrInputPending) {
			t.Fatal(err)
		}
		assertCalls(t, tx.fakeTx, `LoadInputBatch request [{"kind":"cancel","payload":{}}]`, `LoadInputGate request [{"kind":"cancel","payload":{}}]`)
	})
	t.Run("an invalid request touches no storage", func(t *testing.T) {
		service := deviceService(t, &fakeStorage{t: t})
		if _, err := service.SubmitInputs(t.Context(), testTenant, testSession, " ", inputs); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
		if _, err := service.SubmitInputs(t.Context(), testTenant, testSession, inputKey, nil); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	})
}
