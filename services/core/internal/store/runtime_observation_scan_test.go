package store_test

import (
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestRuntimeObservationScanIsDeploymentWideBoundedAndExcludesDeleted(t *testing.T) {
	s, _ := store.NewManagedTestStore(t)
	var expected []string
	for range 5 {
		_, session, _ := managedSession(t, s)
		expected = append(expected, session.ID)
	}
	slices.Sort(expected)
	if err := s.DeleteSession(t.Context(), sessionTenant(t, s, expected[2]), expected[2]); err != nil {
		t.Fatal(err)
	}
	expected = append(expected[:2], expected[3:]...)

	var got []string
	cursor := ""
	for {
		page, err := s.ListRuntimeObservationSessions(t.Context(), cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Sessions) > 2 {
			t.Fatalf("unbounded observation page: %+v", page)
		}
		for _, session := range page.Sessions {
			if session.TenantID == "" || session.SessionID == "" {
				t.Fatalf("incomplete observation identity: %+v", session)
			}
			got = append(got, session.SessionID)
		}
		if page.NextCursor == "" {
			break
		}
		if len(page.Sessions) == 0 || page.NextCursor != page.Sessions[len(page.Sessions)-1].SessionID {
			t.Fatalf("invalid observation cursor: %+v", page)
		}
		cursor = page.NextCursor
	}
	if !slices.Equal(got, expected) {
		t.Fatalf("observation scan = %v, want %v", got, expected)
	}
	if _, err := s.ListRuntimeObservationSessions(t.Context(), "", 0); err == nil {
		t.Fatal("zero observation page size was accepted")
	}
}

func sessionTenant(t *testing.T, s *store.Store, sessionID string) string {
	t.Helper()
	// The deployment-wide scan intentionally discovers tenant identity without
	// enumerating configured API keys. Use that same read to locate this fixture.
	page, err := s.ListRuntimeObservationSessions(t.Context(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range page.Sessions {
		if session.SessionID == sessionID {
			return session.TenantID
		}
	}
	t.Fatalf("Session %s was not listed", sessionID)
	return ""
}
