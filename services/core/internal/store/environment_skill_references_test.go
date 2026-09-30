package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/google/uuid"
)

func TestSkillReferencesFreezeWithinSessionCreation(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{61}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	skillService := SkillService(t, pool, cipher)
	tenant := uuid.NewString()
	first, second := skillArchive(t, "frozen-first"), skillArchive(t, "frozen-second")
	skill, err := skillService.CreateSkill(t.Context(), skills.CreateSkill{TenantID: tenant, Archive: first})
	if err != nil {
		t.Fatal(err)
	}
	skillID := skills.PathID(skill.ID)
	if _, err = skillService.CreateVersion(t.Context(), skills.CreateVersion{TenantID: tenant, SkillID: skillID, Archive: second}); err != nil {
		t.Fatal(err)
	}
	intent := environmentconfig.Setup{Skills: []environmentconfig.Skill{{Metadata: environmentconfig.SkillMetadata{Type: "skill_reference", SkillID: skill.ID}}}}
	input := sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`), Initialization: intent}
	// Concurrent callers share one Session and one frozen installation.
	var group sync.WaitGroup
	ids := make(chan string, 6)
	failures := make(chan error, 6)
	for range 6 {
		group.Add(1)
		go func() {
			defer group.Done()
			session, err := s.CreateSession(t.Context(), tenant, input)
			if err != nil {
				failures <- err
			} else {
				ids <- session.ID
			}
		}()
	}
	group.Wait()
	close(ids)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	sessionID := ""
	for id := range ids {
		if sessionID != "" && sessionID != id {
			t.Fatal("concurrent retry created a second Session")
		}
		sessionID = id
	}
	assertFrozen := func(id, version string, archive []byte) {
		t.Helper()
		setup, err := s.ReadEnvironmentSetup(t.Context(), tenant, id)
		if err != nil || len(setup.Skills) != 1 || setup.Skills[0].Metadata.Type != "skill_reference" || setup.Skills[0].Metadata.SkillID != skill.ID || setup.Skills[0].Metadata.Version != version || !bytes.Equal(setup.Skills[0].Archive, archive) {
			t.Fatal("incorrect frozen installation", err)
		}
		session, err := s.GetSession(t.Context(), tenant, id)
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			Environment struct {
				Skills []environmentconfig.SkillMetadata `json:"skills"`
			} `json:"environment"`
		}
		if json.Unmarshal(session.Configuration, &cfg) != nil || len(cfg.Environment.Skills) != 1 || cfg.Environment.Skills[0] != setup.Skills[0].Metadata || bytes.Contains(session.Configuration, archive) {
			t.Fatal("public snapshot differs from frozen contents")
		}
	}
	assertFrozen(sessionID, "1", first)
	latest := input
	latest.IdempotencyKey = uuid.NewString()
	latest.Initialization.Skills = []environmentconfig.Skill{{Metadata: environmentconfig.SkillMetadata{Type: "skill_reference", SkillID: skill.ID, Version: "latest"}}}
	latestSession, err := s.CreateSession(t.Context(), tenant, latest)
	if err != nil {
		t.Fatal(err)
	}
	assertFrozen(latestSession.ID, "2", second)
	if input.Initialization.Skills[0].Metadata.Version != "" || len(input.Initialization.Skills[0].Archive) != 0 {
		t.Fatal("creation mutated caller intent")
	}
	if _, err = skillService.SetDefaultVersion(t.Context(), skills.SetDefaultVersion{TenantID: tenant, SkillID: skillID, Version: "2"}); err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{"", "latest", "1"} {
		next := input
		next.IdempotencyKey = uuid.NewString()
		next.Initialization.Skills = []environmentconfig.Skill{{Metadata: environmentconfig.SkillMetadata{Type: "skill_reference", SkillID: skill.ID, Version: selector}}}
		created, err := s.CreateSession(t.Context(), tenant, next)
		if err != nil {
			t.Fatal(err)
		}
		if selector == "1" {
			assertFrozen(created.ID, "1", first)
		} else {
			assertFrozen(created.ID, "2", second)
		}
	}
	if err = skillService.DeleteSkill(t.Context(), skills.DeleteSkill{TenantID: tenant, SkillID: skillID}); err != nil {
		t.Fatal(err)
	}
	retry, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil || retry.ID != sessionID {
		t.Fatal("committed retry read deleted sources", err)
	}
	assertFrozen(sessionID, "1", first)
	if _, err = s.ReadEnvironmentSetup(t.Context(), uuid.NewString(), sessionID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("foreign tenant read frozen Skill", err)
	}
}

func TestSkillReferenceAuthorizationRollsBackSession(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{62}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	skill, err := SkillService(t, pool, cipher).CreateSkill(t.Context(), skills.CreateSkill{TenantID: tenant, Archive: skillArchive(t, "private-owner")})
	if err != nil {
		t.Fatal(err)
	}
	input := sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`), Initialization: environmentconfig.Setup{Skills: []environmentconfig.Skill{{Metadata: environmentconfig.SkillMetadata{Type: "skill_reference", SkillID: skill.ID}}}}}
	if _, err := s.CreateSession(t.Context(), foreign, input); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("foreign reference accepted", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE tenant_id=$1", foreign).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed reference left a Session", count, err)
	}
}
