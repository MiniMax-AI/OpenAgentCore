package store

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.Open(t)
	return New(pool), pool
}

// sessionAdapter is the Session adapter on s's database with s's credential
// key. It serves the Session reads and pooled Session storage.
func sessionAdapter(s *Store) *sessionpg.Store { return sessionpg.New(s.pooled, s.credentialCipher) }

// sessionService is the Session service on s's Session adapter. It runs the
// device, heartbeat, enrollment, executor credential and installation use
// cases.
func sessionService(t testing.TB, s *Store) *sessions.Service {
	t.Helper()
	service, err := sessions.NewService(sessionAdapter(s))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// stageTurnArtifacts stages export as the Turn's Artifacts through the Session
// service on s's database, as the execution Worker does.
func stageTurnArtifacts(ctx context.Context, s *Store, tenant, session, turn, environment string, export io.Reader) error {
	service, err := sessions.NewService(sessionAdapter(s))
	if err != nil {
		return err
	}
	return service.StageTurnArtifacts(ctx, sessions.StageTurnArtifactsCommand{TenantID: tenant, SessionID: session, TurnID: turn, EnvironmentID: environment, Export: export})
}

func TestSessionsPersistAndStayTenantScoped(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	tenantA, tenantB := uuid.NewString(), uuid.NewString()
	input := sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", Metadata: map[string]string{"source": "standalone"}, IdempotencyKey: "first",
		Configuration: []byte(`{"agent":{"model":"test-model","instructions":"Keep the snapshot."},"environment":{"type":"none"}}`)}
	first, err := s.CreateSession(ctx, tenantA, input)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateSession(ctx, tenantB, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == other.ID {
		t.Fatal("idempotency leaked across tenants")
	}
	if _, err := s.GetSession(ctx, tenantB, first.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("cross-tenant read: %v", err)
	}
	if _, err := s.ListSessions(ctx, tenantB, first.ID, 10, false, nil); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("cross-tenant cursor: %v", err)
	}
	for _, key := range []string{"second", "third"} {
		input.IdempotencyKey = key
		if _, err := s.CreateSession(ctx, tenantA, input); err != nil {
			t.Fatal(err)
		}
	}
	// Recreate the pool and Store as a new service process would.
	pool.Close()
	recovered, _ := testStore(t)
	got, err := recovered.GetSession(ctx, tenantA, first.ID)
	if err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("restart read = %+v, %v; want %+v", got, err, first)
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := recovered.ListSessions(ctx, tenantA, cursor, 2, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, session := range page.Sessions {
			if session.TenantID != tenantA || seen[session.ID] {
				t.Fatalf("unexpected/duplicate session: %+v", session)
			}
			seen[session.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			t.Fatal("cursor did not advance")
		}
		cursor = page.NextCursor
	}
	if len(seen) != 3 || !seen[first.ID] || seen[other.ID] {
		t.Fatalf("pagination lost or leaked sessions: %+v", seen)
	}
	empty, err := recovered.ListSessions(ctx, uuid.NewString(), "", 10, false, nil)
	if err != nil || empty.Sessions == nil || len(empty.Sessions) != 0 {
		t.Fatalf("empty tenant = %+v, %v", empty, err)
	}
}

func TestConcurrentSessionCreationIsIdempotent(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	input := sessions.CreateSession{Creator: FixtureCreator(), Engine: "fake_alpha", Metadata: map[string]string{"b": "2", "a": "1"}, IdempotencyKey: "repeated"}
	const count = 8
	ids := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, err := s.CreateSession(ctx, tenant, input)
			ids <- session.ID
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	unique := map[string]bool{}
	for id := range ids {
		unique[id] = true
	}
	if len(unique) != 1 {
		t.Fatalf("duplicate sessions: %+v", unique)
	}
	replay, err := s.CreateSession(ctx, tenant, sessions.CreateSession{Creator: FixtureCreator(), Engine: "fake_alpha", Metadata: map[string]string{"a": "1", "b": "2"}, IdempotencyKey: "repeated"})
	if err != nil || !unique[replay.ID] {
		t.Fatalf("reordered metadata was not replayed: %+v %v", replay, err)
	}
	for _, changed := range []sessions.CreateSession{
		{Creator: FixtureCreator(), Engine: "codex", Metadata: input.Metadata, IdempotencyKey: input.IdempotencyKey},
		{Creator: FixtureCreator(), Engine: input.Engine, Metadata: map[string]string{"a": "changed"}, IdempotencyKey: input.IdempotencyKey},
	} {
		if _, err := s.CreateSession(ctx, tenant, changed); !errors.Is(err, sessions.ErrIdempotencyConflict) {
			t.Fatalf("changed request = %v", err)
		}
	}
	page, err := s.ListSessions(ctx, tenant, "", 10, false, nil)
	if err != nil || len(page.Sessions) != 1 || !reflect.DeepEqual(page.Sessions[0], replay) {
		t.Fatalf("retry changed stored session: %+v, %v", page, err)
	}
}
