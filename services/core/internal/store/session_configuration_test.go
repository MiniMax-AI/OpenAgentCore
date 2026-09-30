package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestConfigurationSizeLimitSurvivesJSONBRoundTrip(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	empty := `{"agent":{"model":"example","instructions":""},"environment":{"type":"none"}}`
	raw := strings.Replace(empty, `"instructions":""`, `"instructions":"`+strings.Repeat("x", 512*1024-len(empty))+`"`, 1)
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "size-limit", Configuration: []byte(raw)}
	first, err := s.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSession(ctx, tenant, first.ID)
	if err != nil || string(got.Configuration) != string(first.Configuration) {
		t.Fatalf("configuration failed round trip: %v", err)
	}
	page, err := s.ListSessions(ctx, tenant, "", 10, false, nil)
	if err != nil || len(page.Sessions) != 1 || page.Sessions[0].ID != first.ID {
		t.Fatalf("configuration broke listing: %v", err)
	}
	input.Configuration = append(input.Configuration, ' ')
	if _, err := s.CreateSession(ctx, tenant, input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized request accepted: %v", err)
	}
}

func TestConfigurationIsPartOfSessionIdentity(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "configured",
		Configuration: []byte(`{"agent":{"model":"example","instructions":"First"},"environment":{"type":"none"}}`)}
	first, err := s.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Configuration = []byte(`{"environment": {"type":"none"}, "agent":{"instructions":"First", "model":"example"}}`)
	replay, err := s.CreateSession(ctx, tenant, input)
	if err != nil || replay.ID != first.ID || string(replay.Configuration) != string(first.Configuration) {
		t.Fatalf("equivalent configuration changed identity: %+v, %v", replay, err)
	}
	for _, configuration := range []string{
		`{"agent":{"model":"different","instructions":"First"},"environment":{"type":"none"}}`,
		`{"agent":{"model":"example","instructions":"Changed"},"environment":{"type":"none"}}`,
		`{"agent":{"model":"example","instructions":"First"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`,
	} {
		input.Configuration = []byte(configuration)
		if _, err := s.CreateSession(ctx, tenant, input); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("changed snapshot was accepted: %v", err)
		}
	}
	stored, err := s.GetSession(ctx, tenant, first.ID)
	if err != nil || string(stored.Configuration) != string(first.Configuration) {
		t.Fatalf("retry mutated snapshot: %+v, %v", stored, err)
	}
}

func TestEmptyConfigurationCanonicalizationPreservesCreator(t *testing.T) {
	s, _ := testStore(t)
	tenant := uuid.NewString()
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "empty-configuration"}
	first, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, configuration := range [][]byte{nil, []byte(`{}`), []byte(` { } `)} {
		input.Configuration = configuration
		got, err := s.CreateSession(t.Context(), tenant, input)
		if err != nil || got.ID != first.ID || string(got.Configuration) != "{}" || got.Creator == nil || *got.Creator != FixtureCreator() {
			t.Fatal("canonical retry changed identity or creator", err)
		}
	}
	input.Creator.ID += "-other"
	if _, err := s.CreateSession(t.Context(), tenant, input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("another creator claimed the same request", err)
	}
}
