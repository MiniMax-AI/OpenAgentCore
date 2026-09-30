package store

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// CreateSessionStream shares admission and retry identity with ordinary creation.
// The upsert returns its event cursor while holding the Session write lock, before
// initial inputs commit. No post-commit cursor lookup may skip those inputs.
func (s *Store) CreateSessionStream(ctx context.Context, tenant string, input sessions.CreateSession) (sessions.Creation, error) {
	result, err := s.createSession(ctx, tenant, input)
	if err != nil || !result.Created {
		// A stream retry sends no events, so it needs no projection read.
		return result, err
	}
	result.Session, err = s.sessionActivity(ctx, result.Session, nil)
	if err != nil {
		return sessions.Creation{}, err
	}
	return result, nil
}
