package prototest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Wire scenarios define each Core–Runtime exchange once. Core's gateway test
// replays the Runtime frames against the real gateway, and the Runtime test
// replays the Core frames against the real transport and dispatcher. Each side
// asserts the frames it must send and the behavior it owns.

// Handshake values. The Runtime dials with DeviceID and Credential; Core
// accepts that credential for DeviceID.
const (
	DeviceID    = "runtime"
	Credential  = "synthetic-contract-credential"
	HarnessKind = "contract"
)

// Correlation values the scenarios use.
const (
	SessionID        = "session"
	StateKey         = "agents-api-session"
	PreparationID    = "prepare"
	RunID            = "run"
	CancelDeliveryID = "cancel"
)

// Placeholders for values the Runtime generates. Core's side sends them as
// written; the Runtime's side binds each to the value its Runtime sends.
const (
	ExecutorID       = "executor"
	Handle           = "handle"
	ExpiresAt  int64 = 1_800_000_000_000
)

// Core rejects any other protocol version at the upgrade with this status and
// error code, before any dispatch.
const (
	IncompatibleVersionStatus = http.StatusUpgradeRequired
	IncompatibleVersionCode   = "incompatible_version"
)

// SilenceWindow is how long a Silence or Settle step waits for an unexpected frame.
const SilenceWindow = 50 * time.Millisecond

// IncompatibleVersions are Runtime protocol versions Core rejects.
func IncompatibleVersions() []string {
	return []string{"0.7.0", "0.8.99", "0.8.", proto.Version + "-dev", proto.Version + "+build"}
}

// Peer is one side of the connection.
type Peer string

const (
	Core    Peer = "Core"
	Runtime Peer = "Runtime"
)

// Action is what happens at one scenario step.
type Action int

const (
	// Send: From sends Frame, and the other side receives it next.
	Send Action = iota
	// Silence: From sends nothing for SilenceWindow.
	Silence
	// Settle: the Runtime's native work settles. The Runtime sends nothing
	// before that, so no receipt precedes native settlement.
	Settle
	// Disconnect: the Runtime loses the connection while work runs. The Runtime
	// settles its native work and cleanup; Core observes no execution outcome.
	Disconnect
	// Reconnect: the Runtime connects again with the same device identity.
	Reconnect
)

// Step is one scenario step. From and Frame apply to Send; From applies to Silence.
type Step struct {
	Action Action
	From   Peer
	Frame  proto.Envelope
}

// WireScenario is one ordered exchange.
type WireScenario struct {
	Name string
	// NativeSetupFails makes the Runtime's native Executor setup fail.
	NativeSetupFails bool
	Steps            []Step
}

// CancellationOutcome is the continuity snapshot a cancelled Turn reports.
func CancellationOutcome() proto.DonePayload {
	return proto.DonePayload{Content: "partial", Metadata: map[string]any{proto.DoneMetaAgentSessionID: "native-session"}}
}

// WireScenarios returns every shared exchange.
func WireScenarios() []WireScenario {
	input := proto.TextInput("hello")
	if err := input.Validate(); err != nil {
		panic(err)
	}
	prepare := send(Core, proto.TypeExecutionPrepare, PreparationID, proto.ExecutionPreparePayload{
		SessionID:     SessionID,
		Configuration: proto.PromptRequestPayload{AgentKind: HarnessKind, AgentStateKey: StateKey, StrictResume: true, DisableExecutionEnvironment: true},
	})
	started := []Step{
		prepare,
		status(1, "preparing", "", ""),
		status(2, "ready", "", ""),
		send(Core, proto.TypeExecutionStart, PreparationID, proto.ExecutionStartPayload{ExecutorID: ExecutorID, Handle: Handle, RunID: RunID, Input: input}),
		status(3, "starting", RunID, ""),
		status(4, "started", RunID, ""),
	}
	outcome := CancellationOutcome()
	return []WireScenario{
		{
			Name: "cancellation_waits_for_settlement",
			Steps: append(slices.Clone(started),
				send(Core, proto.TypePromptCancel, RunID, proto.PromptCancelPayload{DeliveryID: CancelDeliveryID}),
				Step{Action: Settle},
				send(Runtime, proto.TypeDone, RunID, proto.DonePayload{}),
				send(Runtime, proto.TypeInteractionDecisionAck, RunID, proto.InteractionDecisionAckPayload{DeliveryID: CancelDeliveryID, Applied: true, Outcome: &outcome}),
			),
		},
		{
			Name:             "preparation_failure_cleans_up_without_run_completion",
			NativeSetupFails: true,
			Steps: []Step{
				prepare,
				status(1, "preparing", "", ""),
				status(2, "failed", "", "preparation_failed"),
				{Action: Silence, From: Runtime},
			},
		},
		{
			Name: "disconnect_is_unknown_and_reconnect_does_not_replay",
			Steps: append(slices.Clone(started),
				Step{Action: Disconnect},
				Step{Action: Reconnect},
				Step{Action: Silence, From: Core},
			),
		},
	}
}

func send(from Peer, kind, id string, payload any) Step {
	frame, err := proto.NewEnvelope(kind, id, payload)
	if err != nil {
		panic(err)
	}
	return Step{Action: Send, From: from, Frame: frame}
}

func status(revision uint64, state, runID, errorCode string) Step {
	return send(Runtime, proto.TypePreparationStatus, PreparationID, proto.PreparationStatusPayload{
		ExecutorID: ExecutorID, Handle: Handle, Revision: revision, State: state, ExpiresAt: ExpiresAt, RunID: runID, ErrorCode: errorCode,
	})
}

// SameFrame checks that got carries want's type, correlation ID and payload.
// Trace is diagnostic correlation and is not part of a scenario.
func SameFrame(want, got proto.Envelope) error {
	return Bindings{}.match(want, got, false)
}

// runtimeAssigned lists the payload fields whose values the Runtime generates.
var runtimeAssigned = []string{"executor_id", "handle", "expires_at"}

// Bindings maps each placeholder to the value the Runtime under test sent for it.
type Bindings map[string]any

// Match checks that got is want's frame. A Runtime-assigned field binds its
// placeholder to the first non-empty value the Runtime sends; later frames must
// repeat that value.
func (b Bindings) Match(want, got proto.Envelope) error {
	return b.match(want, got, true)
}

func (b Bindings) match(want, got proto.Envelope, bind bool) error {
	wantFields, err := fields(want)
	if err != nil {
		return err
	}
	gotFields, err := fields(got)
	if err != nil {
		return err
	}
	bound := map[string]any{}
	if bind {
		for _, field := range runtimeAssigned {
			placeholder, ok := wantFields[field]
			if !ok {
				continue
			}
			key := bindingKey(field, placeholder)
			value, ok := b[key]
			if !ok {
				value = gotFields[field]
				if value == nil || reflect.ValueOf(value).IsZero() {
					return fmt.Errorf("%s frame lacks Runtime-assigned %s", got.Type, field)
				}
				bound[key] = value
			}
			wantFields[field] = value
		}
	}
	if want.Type != got.Type || want.ID != got.ID || !reflect.DeepEqual(wantFields, gotFields) {
		return fmt.Errorf("got %s %q %s, want %s %q %s", got.Type, got.ID, got.Payload, want.Type, want.ID, want.Payload)
	}
	for key, value := range bound {
		b[key] = value
	}
	return nil
}

// Resolve replaces bound placeholders in a Core frame with the Runtime's values.
func (b Bindings) Resolve(frame proto.Envelope) (proto.Envelope, error) {
	payload, err := fields(frame)
	if err != nil {
		return proto.Envelope{}, err
	}
	changed := false
	for _, field := range runtimeAssigned {
		if placeholder, ok := payload[field]; ok {
			if value, ok := b[bindingKey(field, placeholder)]; ok {
				payload[field], changed = value, true
			}
		}
	}
	if !changed {
		return frame, nil
	}
	if frame.Payload, err = json.Marshal(payload); err != nil {
		return proto.Envelope{}, err
	}
	return frame, nil
}

func fields(frame proto.Envelope) (map[string]any, error) {
	out := map[string]any{}
	if len(frame.Payload) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(frame.Payload, &out); err != nil {
		return nil, fmt.Errorf("%s frame payload: %w", frame.Type, err)
	}
	return out, nil
}

func bindingKey(field string, placeholder any) string {
	return fmt.Sprint(field, "=", placeholder)
}
