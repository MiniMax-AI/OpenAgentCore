package sessions

import (
	"encoding/json"
	"regexp"
	"time"

	"github.com/google/uuid"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
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
	// DeploymentProviderRevision is private creation metadata, never retry identity.
	DeploymentProviderRevision uuid.UUID `json:"-"`
	ExecutionConfiguration     *v1.SessionExecutionConfiguration
	ModelProvider              *v1.ModelProviderInput
	ModelProviderSource        string // session, agent or deployment; empty allows only openai_hosted
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

// Creation starts observation at the Session upsert. For a new creation,
// CreateSessionStream returns the committed Session projection that
// CreateSession returns, read after the creation commits; Cursor still precedes
// the initial inputs, so their events remain observable exactly once. Retries
// and FindSessionCreation return only the resource row and its cursor. Only a new creation emits a created snapshot and
// streams from Cursor; a stream retry of an existing creation sends no events.
type Creation struct {
	Session Session
	Created bool
	Cursor  int64
}

func ValidEngine(engine string) bool { return enginePattern.MatchString(engine) }
