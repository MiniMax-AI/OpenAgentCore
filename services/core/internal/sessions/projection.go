package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/items"
)

// Source is an observation the Session projects: an entry of a Turn's journal,
// an admitted input message, or a Turn's terminal outcome.
type Source struct {
	Turn string
	Kind string
	// Sequence is the entry's journal position or the input's sequence; a
	// terminal outcome projected without an entry has none.
	Sequence  int64
	Payload   json.RawMessage
	CreatedAt time.Time
}

// ItemProjectionTx loads and stores the Session Items an observation projects,
// and journals their public changes.
type ItemProjectionTx interface {
	// LoadItem reads what the Session holds for update's Item in the Turn.
	LoadItem(ctx context.Context, turn string, update items.Update) (items.Stored, error)
	// PutItem stores a decided Item change of the Turn, first observed at
	// created, and returns the Item's output index, nil when it has none.
	PutItem(ctx context.Context, turn string, created time.Time, change items.Change) (*int32, error)
	// AppendChanges journals public changes in order.
	AppendChanges(ctx context.Context, changes ...SessionChange) error
}

// ProjectionTx loads and applies what projecting an observation reads and
// writes: Session Items, root Turn usage and Subagents.
type ProjectionTx interface {
	ItemProjectionTx
	SubagentProjectionTx
	// PutTurnUsage replaces a root Turn's recorded usage with a measurement.
	PutTurnUsage(ctx context.Context, turn string, usage v1.TokenUsage) error
}

// InputProjectionTx projects an admitted input.
type InputProjectionTx interface {
	ProjectionTx
	// LoadInputSource reads the Session's admitted input at sequence.
	LoadInputSource(ctx context.Context, sequence int64) (Source, error)
}

// ProjectSource projects one observation under the Session lock. Subagent
// observations update the Session's Subagents, their Turns and Items and the
// root coordination Items. Any other observation replaces its root Turn's
// usage with the measurement it carries, then updates the Session Items it
// projects and journals their changes.
func ProjectSource(ctx context.Context, tx ProjectionTx, source Source) error {
	switch source.Kind {
	case proto.TypeSubagentIdentity:
		return projectSubagentIdentity(ctx, tx, source)
	case proto.TypeSubagentLifecycle:
		return projectSubagentLifecycle(ctx, tx, source.Payload)
	case proto.TypeSubagentTurn:
		return projectSubagentTurn(ctx, tx, source.Payload)
	case proto.TypeSubagentItem:
		return projectSubagentItem(ctx, tx, source.Payload)
	case proto.TypeSubagentCoordination:
		return projectRootCoordination(ctx, tx, source)
	}
	if usage := MeasuredUsage(source.Kind, source.Payload); usage != nil {
		if err := tx.PutTurnUsage(ctx, source.Turn, *usage); err != nil {
			return err
		}
	}
	updates, err := items.Project(source.Turn, source.Kind, source.Sequence, source.Payload)
	if err != nil {
		return fmt.Errorf("project execution item: %w", err)
	}
	for _, update := range updates {
		if err := projectItem(ctx, tx, source, update); err != nil {
			return err
		}
	}
	return nil
}

// ProjectInput projects the Session's admitted input at sequence: a message
// becomes its Session Items, and other inputs project nothing.
func ProjectInput(ctx context.Context, tx InputProjectionTx, sequence int64) error {
	source, err := tx.LoadInputSource(ctx, sequence)
	if err != nil {
		return err
	}
	if source.Kind != "message" {
		return nil
	}
	return ProjectSource(ctx, tx, source)
}

// projectItem applies one Item update source projects: it stores the change
// items.Observe decides and journals the change's public events.
func projectItem(ctx context.Context, tx ItemProjectionTx, source Source, update items.Update) error {
	stored, err := tx.LoadItem(ctx, source.Turn, update)
	if err != nil {
		return err
	}
	change, ok, err := items.Observe(source.Kind, update, stored)
	if err != nil || !ok {
		return err
	}
	index, err := tx.PutItem(ctx, source.Turn, source.CreatedAt, change)
	if err != nil {
		return err
	}
	return tx.AppendChanges(ctx, ItemChanges(change, index)...)
}
