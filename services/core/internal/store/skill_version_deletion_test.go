package store

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/google/uuid"
)

// Deleting a Skill's sole version deletes the Skill, but committed Session
// snapshots and their idempotent retries are unchanged.
func TestSoleSkillVersionDeletionKeepsFrozenSetup(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{63}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	skillService := SkillService(t, pool, cipher)
	tenant := uuid.NewString()
	archive := skillArchive(t, "sole-version-frozen")
	skill, err := skillService.CreateSkill(t.Context(), skills.CreateSkill{TenantID: tenant, Archive: archive})
	if err != nil {
		t.Fatal(err)
	}
	reference := environmentconfig.Setup{Skills: []environmentconfig.Skill{{Metadata: environmentconfig.SkillMetadata{Type: "skill_reference", SkillID: skill.ID}}}}
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`), Initialization: reference}
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := s.ReadEnvironmentSetup(t.Context(), tenant, session.ID)
	if err != nil || len(frozen.Skills) != 1 || frozen.Skills[0].Metadata.Version != "1" || !bytes.Equal(frozen.Skills[0].Archive, archive) {
		t.Fatal("fixture Session did not freeze the sole version", err)
	}
	if _, err = skillService.DeleteVersion(t.Context(), skills.DeleteVersion{TenantID: tenant, SkillID: skills.PathID(skill.ID), Version: 1}); err != nil {
		t.Fatal(err)
	}

	after, err := s.ReadEnvironmentSetup(t.Context(), tenant, session.ID)
	if err != nil || !reflect.DeepEqual(after.Skills, frozen.Skills) {
		t.Fatal("frozen Session installation changed", err)
	}
	retry, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil || retry.ID != session.ID {
		t.Fatal("committed retry read the deleted source", err)
	}
}
