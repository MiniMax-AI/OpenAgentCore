package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
)

// Input is a validated execution command, not an upstream wire type.
// The API validates event fields before constructing this storage input.
type Input struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

type InputReceipt struct {
	Sequence int64
	TurnID   string // Empty for a cancellation accepted while the Session was idle.
	Replayed bool
}

type TurnInput struct {
	Sequence  int64
	Kind      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

// ValidateInputKey enforces the shared request identity limit, including no-op requests.
func ValidateInputKey(key string) error {
	if strings.TrimSpace(key) == "" || len(key) > 128 {
		return fmt.Errorf("%w: idempotency key is required and limited to 128 bytes", ErrInvalidInput)
	}
	return nil
}

// ValidateInputs checks and normalizes an input batch: 1..64 messages,
// cancellations and function results whose nonempty JSON object payloads total
// at most 512 KiB, an empty cancellation payload and a well-formed function
// result. It returns the normalized batch and its encoding, the batch's retry
// identity.
func ValidateInputs(inputs []Input) ([]Input, json.RawMessage, error) {
	if len(inputs) == 0 || len(inputs) > 64 {
		return nil, nil, fmt.Errorf("%w: input batch must contain 1..64 events", ErrInvalidInput)
	}
	batch := make([]Input, len(inputs))
	size := 0
	for i, input := range inputs {
		size += len(input.Payload)
		if size > 512*1024 || len(input.Payload) == 0 || (input.Kind != "message" && input.Kind != "cancel" && input.Kind != "tool_result") {
			return nil, nil, fmt.Errorf("%w: input payloads must be nonempty and total at most 512 KiB", ErrInvalidInput)
		}
		payload, err := jsonobject.Normalize(input.Payload)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
		if input.Kind == "cancel" && string(payload) != "{}" {
			return nil, nil, fmt.Errorf("%w: cancel payload must be empty", ErrInvalidInput)
		}
		if input.Kind == "tool_result" {
			if _, err := ParseFunctionResultInput(payload); err != nil {
				return nil, nil, err
			}
		}
		batch[i] = Input{Kind: input.Kind, Payload: payload}
	}
	encoded, err := json.Marshal(batch)
	return batch, encoded, err
}

// ValidateMessageInputs validates a batch as ValidateInputs does and also
// requires that it holds only messages, as Environment input reservations and
// Session initial input do.
func ValidateMessageInputs(inputs []Input) ([]Input, json.RawMessage, error) {
	for _, input := range inputs {
		if input.Kind != "message" {
			return nil, nil, ErrInvalidInput
		}
	}
	return ValidateInputs(inputs)
}

// decideReplay decides a request whose idempotency key may already hold a
// batch: an unused key admits the request, a key holding the same batch
// replays what it admitted, and a key holding a different batch is
// ErrIdempotencyConflict. The whole batch is the retry identity, so a replay
// never re-evaluates its inputs, such as a cancellation's target.
func decideReplay(used, matches bool) (bool, error) {
	if !used {
		return false, nil
	}
	if !matches {
		return false, ErrIdempotencyConflict
	}
	return true, nil
}

// InputGate is what the Session's Environment input reservations show about a
// new input batch.
type InputGate struct {
	// Matches reports that the Session has no reservation under the batch's
	// idempotency key, or one with the same batch.
	Matches bool
	// Blocked reports that a reservation is pending or one holds the batch's
	// idempotency key.
	Blocked bool
}

// checkInputGate admits a new input batch only while no Environment input
// reservation is pending or holds its key: a reservation holding the key with
// a different batch is ErrIdempotencyConflict, and any other blocking
// reservation ErrInputPending.
func checkInputGate(gate InputGate) error {
	if !gate.Matches {
		return ErrIdempotencyConflict
	}
	if gate.Blocked {
		return ErrInputPending
	}
	return nil
}

// inputPlacement is where admission puts a message or cancellation.
type inputPlacement int

const (
	// steersTurn adds a message to the active Turn.
	steersTurn inputPlacement = iota
	// cancelsTurn adds a cancellation to the active Turn and requests it.
	cancelsTurn
	// startsTurn starts a new Turn with a message on an idle Session.
	startsTurn
	// keepsIdentity records a cancellation on an idle Session without a Turn,
	// which keeps only its retry identity.
	keepsIdentity
)

// placeInput decides where a message or cancellation goes, given whether the
// Session has an active Turn.
func placeInput(kind string, active bool) inputPlacement {
	switch {
	case active && kind == "cancel":
		return cancelsTurn
	case active:
		return steersTurn
	case kind == "message":
		return startsTurn
	}
	return keepsIdentity
}

// InputAdmissionTx is the Session transaction AdmitInput runs in.
type InputAdmissionTx interface {
	FunctionResultTx
	TurnCancellationTx
	InputProjectionTx
	ActiveTurnTx
	// CreateTurn creates a queued Turn in the Session and returns it as
	// stored.
	CreateTurn(ctx context.Context) (Turn, error)
	// CreateTurnInput records input at position of the batch admitted under
	// key, joined to turn, or to no Turn when turn is empty, and returns the
	// Session sequence it allocates.
	CreateTurnInput(ctx context.Context, turn, key string, position int32, input Input) (int64, error)
}

// AdmitInput admits one validated input at position of the batch under key
// and returns its receipt. A function result joins the Turn whose call it
// answers. A message steers the Session's active Turn or, on an idle Session,
// starts a new Turn, which publishes turn.created, then the message's Items,
// then the Session activity. A cancellation requests the cancellation of the
// active Turn; on an idle Session it joins no Turn and keeps only its retry
// identity.
func AdmitInput(ctx context.Context, tx InputAdmissionTx, key string, position int32, input Input) (InputReceipt, error) {
	if input.Kind == "tool_result" {
		result, err := ParseFunctionResultInput(input.Payload)
		if err != nil {
			return InputReceipt{}, err
		}
		turn, err := AdmitFunctionResult(ctx, tx, result)
		if err != nil {
			return InputReceipt{}, err
		}
		sequence, err := tx.CreateTurnInput(ctx, turn.ID, key, position, input)
		if err != nil {
			return InputReceipt{}, err
		}
		return InputReceipt{Sequence: sequence, TurnID: turn.ID}, nil
	}
	turn, active, err := tx.LoadActiveTurn(ctx)
	if err != nil {
		return InputReceipt{}, err
	}
	placement := placeInput(input.Kind, active)
	if placement == startsTurn {
		if turn, err = tx.CreateTurn(ctx); err != nil {
			return InputReceipt{}, err
		}
		if err := tx.AppendChanges(ctx, TurnChanges(turn, true)...); err != nil {
			return InputReceipt{}, err
		}
	}
	sequence, err := tx.CreateTurnInput(ctx, turn.ID, key, position, input)
	if err != nil {
		return InputReceipt{}, err
	}
	if placement == cancelsTurn {
		if err := CancelTurn(ctx, tx, turn); err != nil {
			return InputReceipt{}, err
		}
	}
	if err := ProjectInput(ctx, tx, sequence); err != nil {
		return InputReceipt{}, err
	}
	if placement == startsTurn {
		usage, err := tx.LoadUsage(ctx)
		if err != nil {
			return InputReceipt{}, err
		}
		if err := tx.AppendChanges(ctx, ActivityChange(turn, usage, nil)); err != nil {
			return InputReceipt{}, err
		}
	}
	return InputReceipt{Sequence: sequence, TurnID: turn.ID}, nil
}

// InputTx is the Session transaction of the input use cases.
type InputTx interface {
	InputAdmissionTx
	InputActivityTx
	InputStartTx
	TurnTransitionTx
	// LoadInputBatch reads the receipts of the inputs the Session admitted
	// under key, in batch order and marked replayed, none when it admitted
	// none, and reports whether they are batch.
	LoadInputBatch(ctx context.Context, key string, batch json.RawMessage) ([]InputReceipt, bool, error)
	// LoadInputGate reads what the Session's Environment input reservations
	// show about batch under key.
	LoadInputGate(ctx context.Context, key string, batch json.RawMessage) (InputGate, error)
	// LoadEnvironment reads the Session's Environment; a Session without one
	// is ErrNotFound.
	LoadEnvironment(ctx context.Context) (Environment, error)
	// FindInputReservation reads the Session's Environment input reservation
	// under key, nil when it has none, and reports whether its batch is batch.
	FindInputReservation(ctx context.Context, key string, batch json.RawMessage) (*EnvironmentInputReservation, bool, error)
	// LoadInputReservation reads one of the Session's Environment input
	// reservations; a missing one is ErrNotFound. An admitted reservation
	// carries the receipts of its batch.
	LoadInputReservation(ctx context.Context, reservation string) (EnvironmentInputReservation, error)
	// CreateInputReservation reserves batch under key until the deadline the
	// database clock sets, and returns the pending reservation as stored.
	CreateInputReservation(ctx context.Context, key string, batch json.RawMessage) (EnvironmentInputReservation, error)
	// ExpireInputReservation expires the reservation while it is pending and
	// its deadline has passed by the database clock; otherwise it changes
	// nothing.
	ExpireInputReservation(ctx context.Context, reservation string) error
	// AdmitInputReservation settles the pending reservation as admitted and
	// returns when the database clock settled it.
	AdmitInputReservation(ctx context.Context, reservation string) (time.Time, error)
	// FailInputReservation settles the reservation as failed with code while
	// it is pending; otherwise it changes nothing.
	FailInputReservation(ctx context.Context, reservation, code string) error
	// RecordInputAudit records the send_events write audit of the Session.
	RecordInputAudit(ctx context.Context) error
}

// InputStorage is the pooled storage of the input use cases.
type InputStorage interface {
	// WithInputs runs apply in one pooled transaction under the lock of the
	// tenant's Session and commits only when apply succeeds. A malformed
	// tenant is ErrInvalidInput; a malformed, missing or deleted Session is
	// ErrNotFound.
	WithInputs(ctx context.Context, tenant, session string, apply func(context.Context, InputTx) error) error
}

// InputReader reads a Session's admitted inputs, its Environment input
// reservations and the reservations execution may admit.
type InputReader interface {
	// ListTurnInputs pages a Turn's admitted inputs in sequence order after
	// sequence after, at most limit of 1..100. It is an internal recovery
	// read, not the public event stream. A malformed ID, a negative after and
	// a limit out of range are ErrInvalidInput; a missing Turn is ErrNotFound.
	ListTurnInputs(ctx context.Context, tenant, session, turn string, after int64, limit int) ([]TurnInput, error)
	// GetEnvironmentInputReservation reads one of the tenant's Session's
	// Environment input reservations; an admitted one carries the receipts of
	// its batch. A malformed tenant or reservation ID is ErrInvalidInput; a
	// missing or deleted Session and a missing reservation are ErrNotFound.
	GetEnvironmentInputReservation(ctx context.Context, tenant, session, reservation string) (EnvironmentInputReservation, error)
	// ListEnvironmentInputWork lists, in ID order after the reservation ID
	// after or from the first when it is empty, at most 100 pending
	// reservations before their deadline of live Sessions whose tenant has an
	// unrevoked device among connectedDevices, the Session's bound device when
	// it has one. A malformed ID is ErrInvalidInput.
	ListEnvironmentInputWork(ctx context.Context, after string, connectedDevices []string) ([]EnvironmentInputWork, error)
}

// SubmitInputs admits a request's input batch in order under one Session
// lock and returns a receipt per input. The whole batch under its key is the
// retry identity: a retry replays the receipts of the batch it admitted,
// whatever has changed since, and the same key with a different batch is
// ErrIdempotencyConflict. A batch with a message waits for a pending file
// write to the Session's Environment (ErrTurnConflict), and no batch is
// admitted while an Environment input reservation is pending or holds the key
// (ErrInputPending). Internal receipts are not the response body of the
// public events endpoint.
func (s *Service) SubmitInputs(ctx context.Context, tenant, session, key string, inputs []Input) ([]InputReceipt, error) {
	if err := ValidateInputKey(key); err != nil {
		return nil, err
	}
	batch, encoded, err := ValidateInputs(inputs)
	if err != nil {
		return nil, err
	}
	receipts := make([]InputReceipt, 0, len(batch))
	err = s.storage.WithInputs(ctx, tenant, session, func(ctx context.Context, tx InputTx) error {
		previous, matches, err := tx.LoadInputBatch(ctx, key, encoded)
		if err != nil {
			return err
		}
		replay, err := decideReplay(len(previous) > 0, matches)
		if err != nil {
			return err
		}
		if replay {
			receipts = previous
			return tx.RecordInputAudit(ctx)
		}
		if slices.ContainsFunc(batch, func(input Input) bool { return input.Kind == "message" }) {
			if err := CheckFileWriteGate(ctx, tx); err != nil {
				return err
			}
		}
		gate, err := tx.LoadInputGate(ctx, key, encoded)
		if err != nil {
			return err
		}
		if err := checkInputGate(gate); err != nil {
			return err
		}
		for position, input := range batch {
			receipt, err := AdmitInput(ctx, tx, key, int32(position), input)
			if err != nil {
				return err
			}
			receipts = append(receipts, receipt)
		}
		return tx.RecordInputAudit(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("submit turn inputs: %w", err)
	}
	return receipts, nil
}

// FunctionCall retains public identity and its opaque execution-adapter reference.
type FunctionCall struct {
	CallID, ExecutorCallID, Name string
	Arguments                    json.RawMessage
	Result                       json.RawMessage
	Applied                      bool
}

// FunctionResultInput identifies a persisted call; Result is validated by the API.
// It is an internal command, not an upstream input event. TurnID is the caller's
// value and is resolved within the Session at admission.
type FunctionResultInput struct {
	TurnID string          `json:"turn_id"`
	CallID string          `json:"call_id"`
	Result json.RawMessage `json:"result"`
}
