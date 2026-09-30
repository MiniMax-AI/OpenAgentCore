package sessions

import (
	"encoding/json"
	"errors"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/items"
)

const testSubagent = "5b2d7c61-0d6e-4f0a-9d7e-2f1b3c4d5e6f"

func millis(value int64) *int64 { return &value }

func TestDecideChildTurn(t *testing.T) {
	usage := json.RawMessage(`{"input_tokens":1,"output_tokens":1,"total_tokens":2,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}`)
	ended := ChildTurn{Status: TurnCompleted, CompletedAtMS: millis(2000), Usage: usage}
	for name, test := range map[string]struct {
		old   ChildTurn
		found bool
		next  ChildTurn
		put   bool
		want  error
	}{
		"new":                {ChildTurn{}, false, ChildTurn{Status: TurnInProgress}, true, nil},
		"started":            {ChildTurn{Status: TurnQueued}, true, ChildTurn{Status: TurnInProgress}, true, nil},
		"same status":        {ChildTurn{Status: TurnInProgress}, true, ChildTurn{Status: TurnInProgress}, true, nil},
		"ended":              {ChildTurn{Status: TurnInProgress}, true, ended, true, nil},
		"reopened":           {ChildTurn{Status: TurnInProgress}, true, ChildTurn{Status: TurnQueued}, false, ErrTurnConflict},
		"ended then active":  {ended, true, ChildTurn{Status: TurnInProgress}, false, nil},
		"terminal replay":    {ended, true, ended, false, nil},
		"other status":       {ended, true, ChildTurn{Status: TurnFailed, CompletedAtMS: millis(2000), Usage: usage}, false, ErrIdempotencyConflict},
		"other completion":   {ended, true, ChildTurn{Status: TurnCompleted, CompletedAtMS: millis(2001), Usage: usage}, false, ErrIdempotencyConflict},
		"other usage":        {ended, true, ChildTurn{Status: TurnCompleted, CompletedAtMS: millis(2000)}, false, ErrIdempotencyConflict},
		"reformatted replay": {ended, true, ChildTurn{Status: TurnCompleted, CompletedAtMS: millis(2000), Usage: json.RawMessage(` ` + string(usage))}, false, nil},
	} {
		put, err := decideChildTurn(test.old, test.found, test.next)
		if put != test.put || !errors.Is(err, test.want) || (test.want == nil && err != nil) {
			t.Errorf("%s: put %v, %v", name, put, err)
		}
	}
}

func payload(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// native is a fake LoadNativeSubagent that finds child.
func native(child NativeSubagent) func(string) (NativeSubagent, bool, error) {
	return func(string) (NativeSubagent, bool, error) { return child, true, nil }
}

func TestProjectSubagentIdentity(t *testing.T) {
	name := "reviewer"
	identity := Source{Turn: testTurn, Kind: proto.TypeSubagentIdentity, Sequence: 3, Payload: payload(t, proto.SubagentIdentityPayload{
		NativeID: "child", ParentNativeID: "root", NativeCreatedAt: 1700000000, ParentTurnID: "turn", SourceItemID: "item", Name: &name,
	})}
	t.Run("first observation publishes", func(t *testing.T) {
		var bound []SubagentIdentity
		var changes []SessionChange
		f := &fakeTx{t: t,
			putSubagentIdentity: func(identity SubagentIdentity) (string, error) {
				bound = append(bound, identity)
				return testSubagent, nil
			},
			loadNativeSubagent: native(NativeSubagent{ID: testSubagent}),
			publishSubagent:    done,
			loadPublicSubagent: returns(v1.Subagent{ID: testSubagent}),
			appendChanges:      collect(&changes),
		}
		if err := ProjectSource(t.Context(), f, identity); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f, "PutSubagentIdentity child", "LoadNativeSubagent child", "PublishSubagent "+testSubagent, "LoadPublicSubagent "+testSubagent, "AppendChanges agent.session.subagent.created")
		want := SubagentIdentity{NativeID: "child", ParentNativeID: "root", NativeCreatedAt: 1700000000, FirstTurn: testTurn, FirstOrdinal: 3}
		if len(bound) != 1 || bound[0] != want || changes[0].Event.Subagent.ID != testSubagent {
			t.Fatalf("bound %+v, changes %+v", bound, changes)
		}
	})
	t.Run("published Subagent keeps its metadata", func(t *testing.T) {
		f := &fakeTx{t: t, putSubagentIdentity: func(SubagentIdentity) (string, error) { return testSubagent, nil }, loadNativeSubagent: native(NativeSubagent{ID: testSubagent, Visible: true})}
		if err := ProjectSource(t.Context(), f, identity); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f, "PutSubagentIdentity child", "LoadNativeSubagent child")
	})
	t.Run("self parent", func(t *testing.T) {
		f := &fakeTx{t: t}
		self := Source{Turn: testTurn, Kind: proto.TypeSubagentIdentity, Payload: payload(t, proto.SubagentIdentityPayload{NativeID: "child", ParentNativeID: "child", NativeCreatedAt: 1, ParentTurnID: "turn", SourceItemID: "item"})}
		if err := ProjectSource(t.Context(), f, self); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	})
}

func TestProjectSubagentLifecycle(t *testing.T) {
	active := NativeSubagent{ID: testSubagent, Visible: true, NativeCreatedAt: 1, Status: "active", LifecycleAtMS: 1500}
	closing := Source{Kind: proto.TypeSubagentLifecycle, Payload: payload(t, proto.SubagentLifecyclePayload{NativeID: "child", EffectID: "effect", Status: "closed", OccurredAtMS: 2000})}
	t.Run("status change applies and journals", func(t *testing.T) {
		var applied []SubagentLifecycle
		var changes []SessionChange
		f := &fakeTx{t: t, loadNativeSubagent: native(active), putSubagentEffect: returns(true),
			applySubagentLifecycle: func(lifecycle SubagentLifecycle) error { applied = append(applied, lifecycle); return nil },
			loadPublicSubagent:     returns(v1.Subagent{ID: testSubagent}), appendChanges: collect(&changes)}
		if err := ProjectSource(t.Context(), f, closing); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f, "LoadNativeSubagent child", "PutSubagentEffect effect", "ApplySubagentLifecycle "+testSubagent+" closed", "LoadPublicSubagent "+testSubagent, "AppendChanges agent.session.subagent.closed")
		if len(applied) != 1 || applied[0].AtMS != 2000 || applied[0].ClosedAtMS == nil || *applied[0].ClosedAtMS != 2000 {
			t.Fatalf("applied %+v", applied)
		}
	})
	t.Run("recorded effect is a replay", func(t *testing.T) {
		f := &fakeTx{t: t, loadNativeSubagent: native(active), putSubagentEffect: returns(false)}
		if err := ProjectSource(t.Context(), f, closing); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f, "LoadNativeSubagent child", "PutSubagentEffect effect")
	})
	t.Run("older effect conflicts", func(t *testing.T) {
		later := active
		later.LifecycleAtMS = 2500
		f := &fakeTx{t: t, loadNativeSubagent: native(later), putSubagentEffect: returns(true)}
		if err := ProjectSource(t.Context(), f, closing); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatal(err)
		}
	})
	t.Run("unpublished Subagent", func(t *testing.T) {
		f := &fakeTx{t: t, loadNativeSubagent: native(NativeSubagent{ID: testSubagent})}
		if err := ProjectSource(t.Context(), f, closing); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	})
}

func TestProjectSubagentTurn(t *testing.T) {
	child := NativeSubagent{ID: testSubagent, Visible: true}
	id := items.Identity(testSubagent, "turn:native-turn")
	completed := Source{Kind: proto.TypeSubagentTurn, Payload: payload(t, proto.SubagentTurnPayload{NativeID: "child", TurnID: "native-turn", Status: TurnCompleted, CreatedAtMS: 1000, CompletedAtMS: millis(2000)})}
	t.Run("an ending records activity", func(t *testing.T) {
		f := &fakeTx{t: t, loadNativeSubagent: native(child), loadChildTurn: func() (ChildTurn, bool, error) { return ChildTurn{}, false, nil }, putChildTurn: func(ChildTurn) error { return nil }, recordTerminalActivity: done}
		if err := ProjectSource(t.Context(), f, completed); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f, "LoadNativeSubagent child", "LoadChildTurn "+testSubagent+" "+id, "PutChildTurn "+id+" completed", "RecordTerminalActivity")
	})
	t.Run("a terminal replay records nothing", func(t *testing.T) {
		stored := ChildTurn{ID: id, Subagent: testSubagent, NativeID: "native-turn", Status: TurnCompleted, CreatedAtMS: 1000, CompletedAtMS: millis(2000)}
		f := &fakeTx{t: t, loadNativeSubagent: native(child), loadChildTurn: func() (ChildTurn, bool, error) { return stored, true, nil }}
		if err := ProjectSource(t.Context(), f, completed); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f, "LoadNativeSubagent child", "LoadChildTurn "+testSubagent+" "+id)
	})
	t.Run("a terminal snapshot needs its completion", func(t *testing.T) {
		f := &fakeTx{t: t}
		incomplete := Source{Kind: proto.TypeSubagentTurn, Payload: payload(t, proto.SubagentTurnPayload{NativeID: "child", TurnID: "native-turn", Status: TurnCompleted, CreatedAtMS: 1000})}
		if err := ProjectSource(t.Context(), f, incomplete); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	})
}
