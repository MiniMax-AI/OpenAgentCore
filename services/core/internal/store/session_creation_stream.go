package store

import "context"

// SessionCreation starts observation at the Session upsert. For a new creation,
// CreateSessionStream returns the committed Session projection that
// CreateSession returns, read after the creation commits; Cursor still precedes
// the initial inputs, so their events remain observable exactly once. Retries
// and FindSessionCreation return only the resource row and its cursor. Only a new creation emits a created snapshot and
// streams from Cursor; a stream retry of an existing creation sends no events.
type SessionCreation struct {
	Session Session
	Created bool
	Cursor  int64
}

// CreateSessionStream shares admission and retry identity with ordinary creation.
// The upsert returns its event cursor while holding the Session write lock, before
// initial inputs commit. No post-commit cursor lookup may skip those inputs.
func (s *Store) CreateSessionStream(ctx context.Context, tenant string, input CreateSessionInput) (SessionCreation, error) {
	result, err := s.createSession(ctx, tenant, input)
	if err != nil || !result.Created {
		// A stream retry sends no events, so it needs no projection read.
		return result, err
	}
	result.Session, err = s.sessionActivity(ctx, result.Session, nil)
	if err != nil {
		return SessionCreation{}, err
	}
	return result, nil
}
