package sessions

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/items"
)

// SubagentReader reads the Subagents a visible Session observed, with their
// Turns and Items. A Subagent, Turn or Item outside the named Session or
// Subagent is ErrNotFound. A list limit outside 1..100 is ErrInvalidInput. A
// Subagent or Subagent Turn cursor outside its list, including a malformed
// one, is ErrResourceCursor; an Item cursor is ErrItemCursor.
type SubagentReader interface {
	// GetSubagent returns one Subagent of the Session, including a nested or
	// closed one.
	GetSubagent(ctx context.Context, tenantID, sessionID, subagentID string) (v1.Subagent, error)
	// ListSubagents returns one page of the Session's Subagents in opening
	// order.
	ListSubagents(ctx context.Context, tenantID, sessionID, cursor string, limit int, ascending bool) (v1.SubagentList, error)
	// ListSubagentItems returns one page of the Subagent's Items.
	ListSubagentItems(ctx context.Context, tenantID, sessionID, subagentID, cursor string, limit int, ascending bool) (v1.ItemList, error)
	// GetSubagentTurn returns one of the Subagent's Turns. Its agent_id is the
	// Session's Agent ID and its subagent_id names the child.
	GetSubagentTurn(ctx context.Context, tenantID, sessionID, subagentID, turnID string) (v1.Turn, error)
	// ListSubagentTurns returns one page of the Subagent's Turns in creation
	// order.
	ListSubagentTurns(ctx context.Context, tenantID, sessionID, subagentID, cursor string, limit int, ascending bool) (v1.TurnList, error)
	// ListSubagentTurnItems returns one page of the Items of one of the
	// Subagent's Turns. An empty Turn ID is ErrInvalidInput.
	ListSubagentTurnItems(ctx context.Context, tenantID, sessionID, subagentID, turnID, cursor string, limit int, ascending bool) (v1.ItemList, error)
}

// SubagentIdentity binds a native Subagent to the Session at the journal entry
// that first observed it. It is an execution binding, not a public Subagent.
type SubagentIdentity struct {
	NativeID, ParentNativeID string
	NativeCreatedAt          int64
	FirstTurn                string
	FirstOrdinal             int32
}

// NativeSubagent is what the Session holds for a native Subagent: its ID,
// whether it is public, when it was natively created (in seconds) and its
// lifecycle status as of LifecycleAtMS.
type NativeSubagent struct {
	ID              string
	Visible         bool
	NativeCreatedAt int64
	Status          string
	LifecycleAtMS   int64
}

// SubagentLifecycle is a Subagent's lifecycle status as of AtMS, and when it
// closed.
type SubagentLifecycle struct {
	Status     string
	ClosedAtMS *int64
	AtMS       int64
}

// ChildTurn is a Subagent's Turn. Its timestamps are native milliseconds and
// Usage is its measured usage, nil when unknown.
type ChildTurn struct {
	ID, Subagent, NativeID, Status string
	CreatedAtMS                    int64
	StartedAtMS, CompletedAtMS     *int64
	Usage                          json.RawMessage
}

// StoredChildItem is what the Session holds for a Subagent Item: its Turn,
// position and stored payload, and whether that payload equals the candidate
// it was loaded with.
type StoredChildItem struct {
	Turn     string
	Position int32
	Payload  json.RawMessage
	Same     bool
}

// ChildItem is a Subagent Item to store at its Turn position. Output Items take
// the Turn's next output index when they are first stored.
type ChildItem struct {
	ID, Subagent, Turn string
	Position           int32
	Payload            json.RawMessage
	Output             bool
}

// SubagentProjectionTx loads and applies what projecting Subagent observations
// reads and writes.
type SubagentProjectionTx interface {
	// LoadRootAgent reads the Session's Agent ID.
	LoadRootAgent(ctx context.Context) (string, error)
	// LoadNativeSubagent reads the Session's native Subagent and reports whether
	// the Session has it.
	LoadNativeSubagent(ctx context.Context, native string) (NativeSubagent, bool, error)
	// PutSubagentIdentity binds a native Subagent to the Session, or finds the
	// same binding, and returns its ID. A different binding of the native ID,
	// or a parent the Session does not know, is ErrIdempotencyConflict.
	PutSubagentIdentity(ctx context.Context, identity SubagentIdentity) (string, error)
	// PublishSubagent makes a Subagent public with its identity metadata.
	PublishSubagent(ctx context.Context, id string, name, instructions *string) error
	// LoadPublicSubagent reads a public Subagent of the Session.
	LoadPublicSubagent(ctx context.Context, id string) (v1.Subagent, error)
	// PutSubagentEffect records a native lifecycle effect once and reports
	// whether this call recorded it. Another payload under the same effect ID
	// is ErrIdempotencyConflict.
	PutSubagentEffect(ctx context.Context, effect string, payload json.RawMessage) (bool, error)
	// ApplySubagentLifecycle records a Subagent's lifecycle status.
	ApplySubagentLifecycle(ctx context.Context, id string, lifecycle SubagentLifecycle) error
	// LoadChildTurn reads a Turn of the Subagent and reports whether the
	// Subagent has it.
	LoadChildTurn(ctx context.Context, subagent, id string) (ChildTurn, bool, error)
	// PutChildTurn stores a Subagent Turn. A Turn whose identity differs from
	// the stored one is ErrIdempotencyConflict.
	PutChildTurn(ctx context.Context, turn ChildTurn) error
	// RecordTerminalActivity marks the Session's running managed compute
	// active.
	RecordTerminalActivity(ctx context.Context) error
	// LoadChildItem reads an Item of the Subagent, compares its payload with
	// candidate, and reports whether the Subagent has it.
	LoadChildItem(ctx context.Context, subagent, id string, candidate json.RawMessage) (StoredChildItem, bool, error)
	// PutChildItem stores a Subagent Item.
	PutChildItem(ctx context.Context, item ChildItem) error
}

// validNativeIdentity reports whether a native identifier is a non-empty,
// trimmed single line of at most 512 bytes.
func validNativeIdentity(value string) bool {
	return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

// visibleSubagent reads the public native Subagent an observation names; one
// the Session does not have or has not published is ErrInvalidInput.
func visibleSubagent(ctx context.Context, tx SubagentProjectionTx, native string) (NativeSubagent, error) {
	child, found, err := tx.LoadNativeSubagent(ctx, native)
	if err != nil {
		return NativeSubagent{}, err
	}
	if !found || !child.Visible {
		return NativeSubagent{}, ErrInvalidInput
	}
	return child, nil
}

// recordSubagentChange journals the Subagent's public state as a
// agent.session.subagent.<kind> change.
func recordSubagentChange(ctx context.Context, tx ProjectionTx, id, kind string) error {
	value, err := tx.LoadPublicSubagent(ctx, id)
	if err != nil {
		return err
	}
	return tx.AppendChanges(ctx, SessionChange{Event: v1.SessionEvent{Type: "agent.session.subagent." + kind, Subagent: &value}})
}

// projectSubagentIdentity binds a native Subagent to the Session at the entry
// that observed it and publishes it on its first observation. Identity
// metadata is immutable after its first public observation.
func projectSubagentIdentity(ctx context.Context, tx ProjectionTx, source Source) error {
	var identity proto.SubagentIdentityPayload
	if json.Unmarshal(source.Payload, &identity) != nil || identity.NativeCreatedAt <= 0 || identity.NativeID == identity.ParentNativeID {
		return ErrInvalidInput
	}
	for _, value := range []string{identity.NativeID, identity.ParentNativeID, identity.ParentTurnID, identity.SourceItemID} {
		if !validNativeIdentity(value) {
			return ErrInvalidInput
		}
	}
	id, err := tx.PutSubagentIdentity(ctx, SubagentIdentity{
		NativeID: identity.NativeID, ParentNativeID: identity.ParentNativeID, NativeCreatedAt: identity.NativeCreatedAt,
		FirstTurn: source.Turn, FirstOrdinal: int32(source.Sequence),
	})
	if err != nil {
		return err
	}
	previous, found, err := tx.LoadNativeSubagent(ctx, identity.NativeID)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	if previous.Visible {
		return nil
	}
	if err := tx.PublishSubagent(ctx, id, identity.Name, identity.Instructions); err != nil {
		return err
	}
	return recordSubagentChange(ctx, tx, id, "created")
}

// projectSubagentLifecycle records a public Subagent's lifecycle effect once.
// An effect older than the recorded lifecycle is ErrIdempotencyConflict, and
// only a status change is applied and journaled.
func projectSubagentLifecycle(ctx context.Context, tx ProjectionTx, raw json.RawMessage) error {
	var p proto.SubagentLifecyclePayload
	if json.Unmarshal(raw, &p) != nil || !validNativeIdentity(p.NativeID) || !validNativeIdentity(p.EffectID) || p.OccurredAtMS <= 0 || (p.Status != "active" && p.Status != "closed") {
		return ErrInvalidInput
	}
	child, err := visibleSubagent(ctx, tx, p.NativeID)
	if err != nil {
		return err
	}
	if p.OccurredAtMS < child.NativeCreatedAt*1000 {
		return ErrInvalidInput
	}
	inserted, err := tx.PutSubagentEffect(ctx, p.EffectID, raw)
	if err != nil || !inserted {
		return err
	}
	if p.OccurredAtMS < child.LifecycleAtMS {
		return ErrIdempotencyConflict
	}
	if p.Status == child.Status {
		return nil
	}
	lifecycle := SubagentLifecycle{Status: p.Status, AtMS: p.OccurredAtMS}
	if p.Status == "closed" {
		closed := p.OccurredAtMS
		lifecycle.ClosedAtMS = &closed
	}
	if err := tx.ApplySubagentLifecycle(ctx, child.ID, lifecycle); err != nil {
		return err
	}
	return recordSubagentChange(ctx, tx, child.ID, p.Status)
}

// projectSubagentTurn stores a public Subagent's Turn snapshot. Child Turns
// publish no Session changes: the Session stream carries root work, and child
// state is read through the Subagent routes.
func projectSubagentTurn(ctx context.Context, tx ProjectionTx, raw json.RawMessage) error {
	var p proto.SubagentTurnPayload
	if json.Unmarshal(raw, &p) != nil || !validNativeIdentity(p.NativeID) || !validNativeIdentity(p.TurnID) || p.CreatedAtMS <= 0 {
		return ErrInvalidInput
	}
	if p.Status != TurnQueued && p.Status != TurnInProgress && p.Status != TurnWaiting && !TerminalStatus(p.Status) {
		return ErrInvalidInput
	}
	if TerminalStatus(p.Status) != (p.CompletedAtMS != nil) {
		return ErrInvalidInput
	}
	if (p.StartedAtMS != nil && *p.StartedAtMS < p.CreatedAtMS) || (p.CompletedAtMS != nil && (*p.CompletedAtMS < p.CreatedAtMS || (p.StartedAtMS != nil && *p.CompletedAtMS < *p.StartedAtMS))) {
		return ErrInvalidInput
	}
	child, err := visibleSubagent(ctx, tx, p.NativeID)
	if err != nil {
		return err
	}
	next := ChildTurn{
		ID: items.Identity(child.ID, "turn:"+p.TurnID), Subagent: child.ID, NativeID: p.TurnID, Status: p.Status,
		CreatedAtMS: p.CreatedAtMS, StartedAtMS: p.StartedAtMS, CompletedAtMS: p.CompletedAtMS,
	}
	old, found, err := tx.LoadChildTurn(ctx, child.ID, next.ID)
	if err != nil {
		return err
	}
	if p.Usage != nil {
		value, _ := json.Marshal(p.Usage)
		measured := MeasuredUsage("usage", value)
		if measured == nil {
			return ErrInvalidInput
		}
		next.Usage, _ = json.Marshal(measured)
	}
	put, err := decideChildTurn(old, found, next)
	if err != nil || !put {
		return err
	}
	if err := tx.PutChildTurn(ctx, next); err != nil {
		return err
	}
	// Terminal replays are not stored again, so they never restart the managed
	// idle timer.
	if TerminalStatus(next.Status) {
		return tx.RecordTerminalActivity(ctx)
	}
	return nil
}

// decideChildTurn decides whether a Subagent Turn snapshot is stored over what
// the Session holds. Re-reading native history cannot reopen or change an
// ended child Turn: a snapshot that is not terminal is ignored, the same
// terminal snapshot is a replay, and a different one is
// ErrIdempotencyConflict. A status change ValidTransition rejects is
// ErrTurnConflict.
func decideChildTurn(old ChildTurn, found bool, next ChildTurn) (bool, error) {
	if found && TerminalStatus(old.Status) {
		if !TerminalStatus(next.Status) {
			return false, nil
		}
		var previousUsage, nextUsage *v1.TokenUsage
		_ = json.Unmarshal(old.Usage, &previousUsage)
		_ = json.Unmarshal(next.Usage, &nextUsage)
		if old.Status != next.Status || old.CompletedAtMS == nil || *old.CompletedAtMS != *next.CompletedAtMS || !reflect.DeepEqual(previousUsage, nextUsage) {
			return false, ErrIdempotencyConflict
		}
		return false, nil
	}
	if found && old.Status != next.Status && !ValidTransition(old.Status, next.Status) {
		return false, ErrTurnConflict
	}
	return true, nil
}

// projectSubagentItem stores the Items a public Subagent's Item snapshot
// projects, at twice its native position plus their offset, in a Turn the
// Subagent has.
func projectSubagentItem(ctx context.Context, tx ProjectionTx, raw json.RawMessage) error {
	var p proto.SubagentItemPayload
	if json.Unmarshal(raw, &p) != nil || !validNativeIdentity(p.NativeID) || !validNativeIdentity(p.TurnID) || !validNativeIdentity(p.ItemID) || p.Position < 0 || p.Position > 1073741823 {
		return ErrInvalidInput
	}
	child, err := visibleSubagent(ctx, tx, p.NativeID)
	if err != nil {
		return err
	}
	turnID := items.Identity(child.ID, "turn:"+p.TurnID)
	turn, found, err := tx.LoadChildTurn(ctx, child.ID, turnID)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	projected, err := childItems(ctx, tx, turnID, p)
	if err != nil {
		return err
	}
	for offset, item := range projected {
		if err := putChildItem(ctx, tx, turn, p.Position*2+int32(offset), item); err != nil {
			return err
		}
	}
	return nil
}

// putChildItem stores a Subagent Item at its Turn position. A stored Item
// keeps its Turn and position, and only an in-progress one changes; an Item of
// an ended Turn is ErrTurnConflict. Child Items publish no Session changes:
// the Session stream carries root work, and child history is read through the
// Subagent routes.
func putChildItem(ctx context.Context, tx SubagentProjectionTx, turn ChildTurn, position int32, item v1.Item) error {
	payload, err := item.MarshalStored()
	if err != nil {
		return err
	}
	old, found, err := tx.LoadChildItem(ctx, turn.Subagent, item.ID, payload)
	if err != nil {
		return err
	}
	if found {
		if old.Turn != turn.ID || old.Position != position {
			return ErrIdempotencyConflict
		}
		var previous v1.Item
		if err := json.Unmarshal(old.Payload, &previous); err != nil {
			return err
		}
		if old.Same {
			return nil
		}
		if previous.Status != "in_progress" {
			return ErrIdempotencyConflict
		}
	}
	if TerminalStatus(turn.Status) {
		return ErrTurnConflict
	}
	return tx.PutChildItem(ctx, ChildItem{ID: item.ID, Subagent: turn.Subagent, Turn: turn.ID, Position: position, Payload: payload, Output: item.Role != "user" && item.Type != "function_call_output"})
}

// childItems projects a Subagent Item snapshot to the complete Items it
// carries: a coordination operation, a message, a tool call or a reasoning
// summary.
func childItems(ctx context.Context, tx SubagentProjectionTx, turn string, p proto.SubagentItemPayload) ([]v1.Item, error) {
	var result []v1.Item
	switch p.Kind {
	case proto.TypeSubagentCoordination:
		var value proto.SubagentCoordinationPayload
		if json.Unmarshal(p.Payload, &value) != nil || value.ID != p.ItemID || value.ActorID != p.NativeID {
			return result, ErrInvalidInput
		}
		item, err := coordinationItem(ctx, tx, turn, value)
		return []v1.Item{item}, err
	case proto.TypeOutputMessage:
		var message proto.OutputMessagePayload
		if json.Unmarshal(p.Payload, &message) != nil || message.ID != p.ItemID || message.Text == nil {
			return result, ErrInvalidInput
		}
	case proto.TypeToolCall:
		var call proto.ToolCallPayload
		if json.Unmarshal(p.Payload, &call) != nil || call.ID != p.ItemID || call.Observation == nil {
			return result, ErrInvalidInput
		}
	case "message":
		// Input projection validates and normalizes the existing public message shape.
	case "reasoning":
		var value struct {
			Status  string           `json:"status"`
			Summary []v1.SummaryText `json:"summary"`
		}
		if json.Unmarshal(p.Payload, &value) != nil {
			return result, ErrInvalidInput
		}
		if value.Status != "" && value.Status != "in_progress" && value.Status != "completed" && value.Status != "incomplete" {
			return result, ErrInvalidInput
		}
		for _, part := range value.Summary {
			if part.Type != "summary_text" {
				return result, ErrInvalidInput
			}
		}
		return []v1.Item{{ID: items.Identity(turn, "reasoning:"+p.ItemID), TurnID: turn, Type: "reasoning", Status: value.Status, Summary: value.Summary}}, nil
	default:
		return result, ErrInvalidInput
	}
	updates, err := items.Project(turn, p.Kind, int64(p.Position), p.Payload)
	if err != nil {
		return result, err
	}
	if len(updates) < 1 || len(updates) > 2 {
		return nil, ErrInvalidInput
	}
	for _, update := range updates {
		if update.AppendText {
			return nil, ErrInvalidInput
		}
		result = append(result, update.Item)
	}
	return result, nil
}

// coordinationItem projects a native coordination operation to its Item in the
// Turn, resolving native actors and recipients to public Agent and Subagent
// IDs. An empty native ID is the Session's Agent. A failed operation may name a
// recipient the Session never observed, which keeps its native ID; any other
// unknown or unpublished Subagent is ErrNotFound.
func coordinationItem(ctx context.Context, tx SubagentProjectionTx, turn string, p proto.SubagentCoordinationPayload) (v1.Item, error) {
	value := v1.Item{ID: items.Identity(turn, "coordination:"+p.ID), TurnID: turn, Type: p.Kind, Status: p.Status, Model: p.Model, ReasoningEffort: p.ReasoningEffort}
	if !validNativeIdentity(p.ID) {
		return value, ErrInvalidInput
	}
	if p.Status != "in_progress" && p.Status != "completed" && p.Status != "failed" && p.Status != "incomplete" && p.Kind != "agent_message" {
		return value, ErrInvalidInput
	}
	resolve := func(native string, failedReference bool) (string, error) {
		if native == "" {
			return tx.LoadRootAgent(ctx)
		}
		child, found, err := tx.LoadNativeSubagent(ctx, native)
		if err != nil {
			return "", err
		}
		if !found {
			if failedReference && validNativeIdentity(native) {
				return native, nil
			}
			return "", ErrNotFound
		}
		if !child.Visible {
			return "", ErrNotFound
		}
		return child.ID, nil
	}
	actor, err := resolve(p.ActorID, false)
	if err != nil {
		return value, err
	}
	if actor == "" {
		return value, ErrInvalidInput
	}
	value.SenderAgentID = actor
	if p.Text != nil {
		value.Content = []v1.ItemContent{{Type: "output_text", Text: p.Text}}
	}
	switch p.Kind {
	case "create_subagent_call":
		value.AgentID = actor
	case "wait_for_subagents_call":
		value.RecipientAgentIDs = []string{}
		for _, recipient := range p.Recipients {
			id, err := resolve(recipient, p.Status == "failed")
			if err != nil {
				return value, err
			}
			value.RecipientAgentIDs = append(value.RecipientAgentIDs, id)
		}
	case "send_subagent_input_call", "resume_subagent_call", "interrupt_subagent_call", "close_subagent_call", "agent_message":
		if len(p.Recipients) != 1 {
			return value, ErrInvalidInput
		}
		value.RecipientAgentID, err = resolve(p.Recipients[0], p.Status == "failed")
		if err != nil {
			return value, err
		}
	default:
		return value, ErrInvalidInput
	}
	return value, nil
}

// projectRootCoordination projects the root Agent's coordination operation to
// its Session Item and journals the Item's changes.
func projectRootCoordination(ctx context.Context, tx ProjectionTx, source Source) error {
	var p proto.SubagentCoordinationPayload
	if json.Unmarshal(source.Payload, &p) != nil || p.ActorID != "" {
		return ErrInvalidInput
	}
	value, err := coordinationItem(ctx, tx, source.Turn, p)
	if err != nil {
		return err
	}
	return projectItem(ctx, tx, source, items.Update{Item: value})
}
