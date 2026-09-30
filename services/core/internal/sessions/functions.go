package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
)

// maxFunctionArguments bounds the arguments of a recorded function call.
const maxFunctionArguments = 512 * 1024

// validFunctionIdentity checks a function call's public or executor identity or
// its name.
func validFunctionIdentity(id string) bool { return strings.TrimSpace(id) != "" && len(id) <= 512 }

// validateFunctionCall checks a call an executor reported before it is
// recorded: identities and name within bounds, JSON arguments within bounds and
// no result or application receipt yet.
func validateFunctionCall(call FunctionCall) error {
	if !validFunctionIdentity(call.CallID) || !validFunctionIdentity(call.ExecutorCallID) || !validFunctionIdentity(call.Name) ||
		len(call.Arguments) > maxFunctionArguments || !json.Valid(call.Arguments) || call.Result != nil || call.Applied {
		return ErrInvalidInput
	}
	return nil
}

// ParseFunctionResultInput decodes a tool_result input payload: a Turn ID, a
// call identity and a result object, which it normalizes. It does not check
// that the Turn or call exists; AdmitFunctionResult resolves them.
func ParseFunctionResultInput(raw json.RawMessage) (FunctionResultInput, error) {
	var input FunctionResultInput
	if json.Unmarshal(raw, &input) != nil || input.TurnID == "" || !validFunctionIdentity(input.CallID) || len(input.Result) == 0 {
		return input, ErrInvalidInput
	}
	result, err := jsonobject.Normalize(input.Result)
	if err != nil {
		return input, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	input.Result = result
	return input, nil
}

// acceptsFunctionResults reports whether a Turn takes new function calls,
// results and application receipts: it is in progress or waiting, and no
// cancellation was requested.
func acceptsFunctionResults(turn Turn) bool {
	return (turn.Status == TurnInProgress || turn.Status == TurnWaiting) && turn.CancelRequestedAt.IsZero()
}

// FunctionResultMatch is what a Turn's stored call shows about a submitted
// result.
type FunctionResultMatch struct {
	// Recorded reports that the Turn has the call.
	Recorded bool
	// Submitted reports that the call already has a result.
	Submitted bool
	// Matches reports that the saved result equals the submitted one.
	Matches bool
}

// decideFunctionResult decides whether to store a submitted result for a call
// the Turn has. An identical retry of a saved result stores nothing, even after
// the Turn ended; a different result conflicts with the saved one. A new result
// needs a Turn that accepts results.
func decideFunctionResult(turn Turn, match FunctionResultMatch) (bool, error) {
	if match.Submitted {
		if !match.Matches {
			return false, ErrFunctionResultConflict
		}
		return false, nil
	}
	if !acceptsFunctionResults(turn) {
		return false, ErrTurnConflict
	}
	return true, nil
}

// FunctionResultTx is the Session transaction a function result is admitted in.
type FunctionResultTx interface {
	// FindResultTurn reads the Session's Turn that a result names and reports
	// whether the Session has it. A malformed ID names no Turn.
	FindResultTurn(ctx context.Context, turn string) (Turn, bool, error)
	// MatchFunctionResult compares result with the Turn's call.
	MatchFunctionResult(ctx context.Context, turn, call string, result json.RawMessage) (FunctionResultMatch, error)
	// SubmitFunctionResult saves the result of the Turn's call.
	SubmitFunctionResult(ctx context.Context, turn, call string, result json.RawMessage) error
	// HasFunctionCall reports whether any Turn of the Session has the call.
	HasFunctionCall(ctx context.Context, call string) (bool, error)
}

// AdmitFunctionResult saves a submitted function result for its Turn's call
// and returns that Turn, which the result's input joins. It never creates a
// Turn. A result whose Turn does not have the call is
// ErrFunctionCallTurnMismatch when another Turn of the Session has it and
// ErrUnknownFunctionCall otherwise; the answer depends only on the Session's
// own calls.
func AdmitFunctionResult(ctx context.Context, tx FunctionResultTx, input FunctionResultInput) (Turn, error) {
	turn, found, err := tx.FindResultTurn(ctx, input.TurnID)
	if err != nil {
		return Turn{}, err
	}
	if found {
		match, err := tx.MatchFunctionResult(ctx, turn.ID, input.CallID, input.Result)
		if err != nil {
			return Turn{}, err
		}
		if match.Recorded {
			submit, err := decideFunctionResult(turn, match)
			if err != nil {
				return Turn{}, err
			}
			if submit {
				if err := tx.SubmitFunctionResult(ctx, turn.ID, input.CallID, input.Result); err != nil {
					return Turn{}, err
				}
			}
			return turn, nil
		}
	}
	elsewhere, err := tx.HasFunctionCall(ctx, input.CallID)
	if err != nil {
		return Turn{}, err
	}
	if elsewhere {
		return Turn{}, ErrFunctionCallTurnMismatch
	}
	return Turn{}, ErrUnknownFunctionCall
}

// PendingFunctionCallsTx reads a Turn's function calls that wait for a result
// or its application receipt.
type PendingFunctionCallsTx interface {
	// LoadPendingFunctionCalls reads the Turn's calls without an application
	// receipt, in creation order, while the Turn is in progress or waiting and
	// no cancellation was requested, and none otherwise.
	LoadPendingFunctionCalls(ctx context.Context, turn string) ([]FunctionCall, error)
}

// LoadRequiredActions returns the function calls a Turn requires the caller to
// answer as public required actions: its pending calls while it accepts
// results, and none otherwise.
func LoadRequiredActions(ctx context.Context, tx PendingFunctionCallsTx, turn Turn) ([]v1.FunctionCallAction, error) {
	actions := make([]v1.FunctionCallAction, 0)
	if !acceptsFunctionResults(turn) {
		return actions, nil
	}
	calls, err := tx.LoadPendingFunctionCalls(ctx, turn.ID)
	if err != nil {
		return nil, err
	}
	for _, call := range calls {
		actions = append(actions, v1.FunctionCallAction{Type: "function_call", CallID: call.CallID, Name: call.Name, TurnID: turn.ID, Arguments: call.Arguments})
	}
	return actions, nil
}
