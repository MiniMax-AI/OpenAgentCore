package sessionpg

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// Store is the pooled Session adapter: the storage of the Session use cases
// and the Session reads.
type Store struct{ units *pgunit.Pool }

// New builds the pooled Session adapter on units.
func New(units *pgunit.Pool) *Store { return &Store{units: units} }

var (
	_ sessions.Storage = (*Store)(nil)
	_ sessions.Reader  = (*Store)(nil)
)
