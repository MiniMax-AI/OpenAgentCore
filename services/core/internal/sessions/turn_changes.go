package sessions

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/items"
)

// TurnChanges returns the public change that reports a Turn's new status:
// agent.session.turn.created for a newly admitted Turn and
// agent.session.turn.<status> otherwise. A waiting Turn reports nothing; its
// Session activity carries the required actions. EndTurn reports a Turn that
// ended together with what it settles.
func TurnChanges(turn Turn, created bool) []SessionChange {
	if turn.Status == TurnWaiting {
		return nil
	}
	turn.Outcome = nil
	kind := turn.Status
	if created {
		kind = "created"
	}
	return []SessionChange{{Event: v1.SessionEvent{Type: "agent.session.turn." + kind, TurnID: turn.ID}, Turn: &turn}}
}

// ActivityChange returns the Session activity snapshot after a Turn change:
// requires_action while the Turn waits on actions, idle once it ended, failed
// when it failed, and in_progress otherwise. usage is the Session's public
// usage. The snapshot of a Turn that ended is settled: it marks that Turn's
// end, while an input reserved during its Artifact capture is newer work.
func ActivityChange(turn Turn, usage json.RawMessage, actions []v1.FunctionCallAction) SessionChange {
	turn.Outcome = nil
	status := "in_progress"
	if TerminalStatus(turn.Status) {
		status = "idle"
		if turn.Status == TurnFailed {
			status = "failed"
		}
	} else if len(actions) > 0 {
		status = "requires_action"
	}
	return SessionChange{
		Event: v1.SessionEvent{Type: "agent.session." + status}, Turn: &turn,
		SessionUsage: usage, RequiredActions: actions, Settled: TerminalStatus(turn.Status),
	}
}

// ArtifactSettlement is what a Turn that ended does with the Artifacts it
// staged.
type ArtifactSettlement string

const (
	// PublishArtifacts publishes the staged Artifacts at the Turn's completion
	// time. A path whose newest remaining Artifact has the same bytes keeps that
	// Artifact instead; new paths, changed bytes and paths whose newest Artifact
	// was deleted publish.
	PublishArtifacts ArtifactSettlement = "publish"
	// DiscardArtifacts deletes the staged Artifacts.
	DiscardArtifacts ArtifactSettlement = "discard"
)

// UnfinishedItem is an Item still in progress when its Turn ends.
type UnfinishedItem struct {
	// Position is the Item's Session position.
	Position int32
	// OutputIndex is the Item's output index, nil when it has none.
	OutputIndex *int32
	Item        v1.Item
}

// Ending holds the facts a Turn that ended is settled with, read under the
// Session lock in the transaction that ends it.
type Ending struct {
	// Unfinished are the Turn's Items still in progress, in any order.
	Unfinished []UnfinishedItem
	// Usage is the Session's public usage with the Turn ended.
	Usage json.RawMessage
}

// TurnEnd is what a Turn that ended writes in the transaction that ends it,
// applied in field order.
type TurnEnd struct {
	// TerminalActivity marks managed compute activity when Core commits the
	// end, so managed idle time starts then and never on a Runtime clock.
	TerminalActivity bool
	Artifacts        ArtifactSettlement
	// PublishedAt is the publication time of PublishArtifacts.
	PublishedAt time.Time
	// FinishedItems are the IDs of the Turn's unfinished Items. Each takes
	// FinishedStatus, and all of them share one settlement time.
	FinishedItems  []string
	FinishedStatus string
	// Changes are the public changes: each finished Item's events in Session
	// position order, the Turn's event, then the Session's settled activity.
	Changes []SessionChange
}

// EndTurn decides what a Turn that ended settles. A completed Turn publishes
// its Artifacts and a failed or cancelled one discards them. Its unfinished
// Items become incomplete, keeping their partial content, so none is reported
// as a success the Turn never produced.
func EndTurn(turn Turn, ending Ending) TurnEnd {
	end := TurnEnd{TerminalActivity: true, FinishedStatus: "incomplete"}
	switch turn.Status {
	case TurnCompleted:
		end.Artifacts, end.PublishedAt = PublishArtifacts, turn.CompletedAt
	case TurnFailed, TurnCancelled:
		end.Artifacts = DiscardArtifacts
	}
	unfinished := slices.Clone(ending.Unfinished)
	slices.SortStableFunc(unfinished, func(a, b UnfinishedItem) int { return cmp.Compare(a.Position, b.Position) })
	for _, open := range unfinished {
		finished := open.Item
		finished.Status = end.FinishedStatus
		end.FinishedItems = append(end.FinishedItems, open.Item.ID)
		end.Changes = append(end.Changes, ItemChanges(items.Change{Previous: open.Item, Item: finished}, open.OutputIndex)...)
	}
	end.Changes = append(end.Changes, TurnChanges(turn, false)...)
	end.Changes = append(end.Changes, ActivityChange(turn, ending.Usage, nil))
	return end
}
