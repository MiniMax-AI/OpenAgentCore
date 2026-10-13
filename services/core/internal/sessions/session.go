package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/metadata"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
)

var enginePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// Session is a durable execution context, separate from product conversations
// and from live daemon connections. Engine session IDs will be bound at execution.
type Session struct {
	ID                       string
	TenantID                 string
	Creator                  *identity.Subject
	Engine                   string
	Metadata                 map[string]string
	CreatedAt                time.Time
	Configuration            json.RawMessage
	LastTurn                 *Turn
	Usage                    json.RawMessage
	RequiredActions          []v1.FunctionCallAction
	Environment              *Environment
	EnvironmentInputActivity *EnvironmentInputActivity
	// EnvironmentFailure is the recorded provisioning failure of a failed hosted
	// Environment. It makes the Session failed and is terminal.
	EnvironmentFailure *EnvironmentFailure
	// PendingInput reports that the latest input reservation, read once no Turn
	// is active or newer, can still start a Turn. It only supports settlement
	// checks and is never rendered.
	PendingInput bool
}

type CreateSession struct {
	// SupportsRetainedNativeHistory is trusted admission input from the immutable engine profile.
	// It is not persisted and never contributes to the caller's retry identity.
	SupportsRetainedNativeHistory bool `json:"-"`
	// DeploymentProviderRevision is private creation metadata, never retry identity.
	DeploymentProviderRevision uuid.UUID `json:"-"`
	ExecutionConfiguration     *v1.SessionExecutionConfiguration
	ModelProvider              *v1.ModelProviderInput
	ModelProviderSource        v1.ExecutionSource // session, agent or deployment; empty allows only openai_hosted
	Initialization             environmentconfig.Setup
	InitialFiles               []environmentconfig.InitialFile
	Creator                    identity.Subject
	CreationRequest            json.RawMessage
	Engine                     string
	Metadata                   map[string]string
	IdempotencyKey             string
	Configuration              json.RawMessage
	InitialInputs              []Input
}

type Page struct {
	Sessions   []Session
	NextCursor string
}

// Creation starts observation at the Session upsert. Session is the Session
// projection the creation transaction read last, after any initial input;
// Cursor is the event sequence the upsert returned, before that input, so its
// events remain observable exactly once. Only a new creation emits a created
// snapshot and streams from Cursor; a stream retry of an existing creation
// sends no events. FindSessionCreation returns only the Session ID.
type Creation struct {
	Session Session
	Created bool
	Cursor  int64
}

func ValidEngine(engine string) bool { return enginePattern.MatchString(engine) }

// SessionStorage persists the public Session writes.
type SessionStorage interface {
	// WithSessionDeletion runs apply in one Session transaction of the
	// tenant's Session, publicly deleted or not, with what the Session lock
	// shows. It prunes the Session's journal after apply and commits only
	// when both succeed. A malformed tenant is ErrInvalidInput; a malformed or
	// missing Session is ErrNotFound.
	WithSessionDeletion(ctx context.Context, tenantID, sessionID string, apply func(context.Context, LockedSession, SessionDeletionTx) error) error
	// UpdateSessionMetadata replaces the visible Session's metadata with the
	// encoded object, records the write audit and reads the Session as
	// GetSession does, in one transaction, or returns ErrNotFound.
	UpdateSessionMetadata(ctx context.Context, tenantID, sessionID string, encoded []byte) (Session, error)
	// AuditSessionOperation records the write audit of action on the visible
	// Session under its lock, or returns ErrNotFound.
	AuditSessionOperation(ctx context.Context, tenantID, sessionID, action string) error
}

// SessionDeletionTx is the Session transaction DeleteSession runs in.
type SessionDeletionTx interface {
	ActiveTurnTx
	// LoadEnvironmentInput reads the Session's latest Environment input
	// reservation that no Turn has admitted or superseded, nil when there is
	// none.
	LoadEnvironmentInput(ctx context.Context) (*EnvironmentInputState, error)
	// ApplyDeletion deletes the Session's Artifacts, releases the node
	// placement of its Environments that have no allocation and removes the
	// Session from public access. Its row stays so that execution can settle.
	ApplyDeletion(ctx context.Context) error
	// RecordDeletionAudit records the write audit of the deletion.
	RecordDeletionAudit(ctx context.Context) error
}

// settled reports whether a Session may be deleted: it has no queued,
// in-progress or waiting Turn, which includes pending required actions and
// function results, and no pending input reservation, such as queued later
// input, self-hosted input awaiting a connection, or hosted initial input while
// provisioning. Terminal idle and failed Sessions are settled.
func settled(activeTurn bool, input *EnvironmentInputState) bool {
	_, pending := InputActivity(input)
	return !activeTurn && !pending
}

// DeleteSessionCommand deletes one Session.
type DeleteSessionCommand struct {
	TenantID  string
	SessionID string
}

// DeleteSession removes public access to a settled Session while keeping the
// state execution needs to settle. It decides under the Session lock that also
// orders Turn and input admission, so a concurrent admission either commits
// first and is rejected here with ErrNotIdle, or observes the deletion. A
// repeated deletion only records the write audit; foreign and missing Sessions
// are ErrNotFound.
func (s *Service) DeleteSession(ctx context.Context, command DeleteSessionCommand) error {
	return s.storage.WithSessionDeletion(ctx, command.TenantID, command.SessionID, func(ctx context.Context, locked LockedSession, tx SessionDeletionTx) error {
		if !locked.Deleted {
			_, active, err := tx.LoadActiveTurn(ctx)
			if err != nil {
				return err
			}
			var input *EnvironmentInputState
			if !active {
				if input, err = tx.LoadEnvironmentInput(ctx); err != nil {
					return err
				}
			}
			if !settled(active, input) {
				return ErrNotIdle
			}
			if err := tx.ApplyDeletion(ctx); err != nil {
				return err
			}
		}
		return tx.RecordDeletionAudit(ctx)
	})
}

// UpdateSessionMetadataCommand replaces a Session's metadata.
type UpdateSessionMetadataCommand struct {
	TenantID  string
	SessionID string
	Metadata  map[string]string
}

// UpdateSessionMetadata replaces the visible Session's metadata and returns
// the Session. Invalid metadata is ErrInvalidInput.
func (s *Service) UpdateSessionMetadata(ctx context.Context, command UpdateSessionMetadataCommand) (Session, error) {
	encoded, err := metadata.Encode(command.Metadata)
	if err != nil {
		return Session{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	return s.storage.UpdateSessionMetadata(ctx, command.TenantID, command.SessionID, encoded)
}

// AuditSessionOperationCommand records an authorized public operation that
// changes no Session: a no-op or a creation replay.
type AuditSessionOperationCommand struct {
	TenantID  string
	SessionID string
	// Action is create or send_events.
	Action string
}

// AuditSessionOperation records the write audit of a public no-op or creation
// replay on the visible Session. It cannot create ownership or admit execution
// work. Another action is ErrInvalidInput.
func (s *Service) AuditSessionOperation(ctx context.Context, command AuditSessionOperationCommand) error {
	if command.Action != string(writeaudit.ActionCreate) && command.Action != string(writeaudit.ActionSendEvents) {
		return ErrInvalidInput
	}
	return s.storage.AuditSessionOperation(ctx, command.TenantID, command.SessionID, command.Action)
}
