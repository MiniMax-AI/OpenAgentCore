package store

import (
	"bytes"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/google/uuid"
)

func TestSkillListsAcceptLimitZero(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{43}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	archive := skillArchive(t, "limit-zero")
	created, err := s.CreateSkill(t.Context(), tenant, archive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteSkill(t.Context(), tenant, created.ID) })
	if _, err = s.CreateSkillVersion(t.Context(), tenant, created.ID, archive, false); err != nil {
		t.Fatal(err)
	}

	// A zero page is empty; HasMore reports whether a resource follows the cursor.
	for _, test := range []struct {
		tenant, after string
		hasMore       bool
	}{{tenant, "", true}, {tenant, created.ID, false}, {foreign, "", false}} {
		page, err := s.ListSkills(t.Context(), test.tenant, test.after, 0, true)
		if err != nil || len(page.Skills) != 0 || page.HasMore != test.hasMore {
			t.Fatalf("zero Skill page after %q: %+v %v", test.after, page, err)
		}
	}
	versions, err := s.ListSkillVersions(t.Context(), tenant, created.ID, "", 100, true)
	if err != nil || len(versions.Versions) != 2 {
		t.Fatal("versions", versions, err)
	}
	for after, hasMore := range map[string]bool{"": true, versions.Versions[0].ID: true, versions.Versions[1].ID: false} {
		page, err := s.ListSkillVersions(t.Context(), tenant, created.ID, after, 0, true)
		if err != nil || len(page.Versions) != 0 || page.HasMore != hasMore {
			t.Fatalf("zero version page after %q: %+v %v", after, page, err)
		}
	}

	// Foreign cursors and parents stay indistinguishable from missing ones.
	if _, err = s.ListSkills(t.Context(), foreign, created.ID, 0, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign cursor", err)
	}
	if _, err = s.ListSkillVersions(t.Context(), foreign, created.ID, "", 0, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign versions", err)
	}
	for _, limit := range []int{-1, 101} {
		if _, err = s.ListSkills(t.Context(), tenant, "", limit, true); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("Skill limit", limit, err)
		}
		if _, err = s.ListSkillVersions(t.Context(), tenant, created.ID, "", limit, true); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("version limit", limit, err)
		}
	}
}
