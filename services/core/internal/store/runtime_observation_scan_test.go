package store_test

import (
	"slices"
	"testing"
)

func TestRuntimeObservationScanIsDeploymentWideBoundedAndExcludesDeleted(t *testing.T) {
	s, db := newManagedTestStoreDB(t)
	var expected []string
	for range 5 {
		_, session, _ := managedSession(t, s, db)
		expected = append(expected, session.ID)
	}
	slices.Sort(expected)
	if err := s.DeleteSession(t.Context(), sessionTenant(t, db, expected[2]), expected[2]); err != nil {
		t.Fatal(err)
	}
	expected = append(expected[:2], expected[3:]...)

	var got []string
	cursor := ""
	for {
		page, err := fixtureReader(db).ObservationSessions(t.Context(), cursor, 2)
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
	if _, err := fixtureReader(db).ObservationSessions(t.Context(), "", 0); err == nil {
		t.Fatal("zero observation page size was accepted")
	}
}

func sessionTenant(t *testing.T, db fixtureDB, sessionID string) string {
	t.Helper()
	// The deployment-wide scan intentionally discovers tenant identity without
	// enumerating configured API keys. Use that same read to locate this fixture.
	page, err := fixtureReader(db).ObservationSessions(t.Context(), "", 100)
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
