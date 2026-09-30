package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func skillVersionDeletionStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{63}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return NewWithCredentialCipher(pool, cipher), pool
}

func skillRowCounts(t *testing.T, pool *pgxpool.Pool, skillID string) (skills, versions int) {
	t.Helper()
	id := uuid.MustParse(skillID[len("skill_"):])
	if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM skills WHERE id=$1), (SELECT count(*) FROM skill_versions WHERE skill_id=$1)", id).Scan(&skills, &versions); err != nil {
		t.Fatal(err)
	}
	return skills, versions
}

func TestSoleSkillVersionDeletionRemovesSkill(t *testing.T) {
	s, pool := skillVersionDeletionStore(t)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	archive := skillArchive(t, "sole-version-frozen")
	skill, err := s.CreateSkill(t.Context(), tenant, archive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteSkill(t.Context(), tenant, skill.ID) })
	version, err := s.GetSkillVersion(t.Context(), tenant, skill.ID, "1")
	if err != nil {
		t.Fatal(err)
	}
	reference := environmentconfig.Setup{Skills: []environmentconfig.Skill{{Metadata: environmentconfig.SkillMetadata{Type: "skill_reference", SkillID: skill.ID}}}}
	template, err := s.CreateEnvironmentTemplate(t.Context(), tenant, EnvironmentTemplateInput{SetSkills: true, Initialization: reference})
	if err != nil {
		t.Fatal(err)
	}
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`), Initialization: reference}
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := s.ReadEnvironmentSetup(t.Context(), tenant, session.ID)
	if err != nil || len(frozen.Skills) != 1 || frozen.Skills[0].Metadata.Version != "1" || !bytes.Equal(frozen.Skills[0].Archive, archive) {
		t.Fatal("fixture Session did not freeze the sole version", err)
	}

	// Foreign and missing targets fail before any mutation.
	if _, err = s.DeleteSkillVersion(t.Context(), foreign, skill.ID, "1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign sole-version deletion", err)
	}
	if _, err = s.DeleteSkillVersion(t.Context(), tenant, skill.ID, "2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing version deletion", err)
	}
	if skills, versions := skillRowCounts(t, pool, skill.ID); skills != 1 || versions != 1 {
		t.Fatal("rejected deletion changed rows", skills, versions)
	}

	deleted, err := s.DeleteSkillVersion(t.Context(), tenant, skill.ID, "1")
	if err != nil || deleted.ID != version.ID || deleted.SkillID != skill.ID || deleted.Version != 1 {
		t.Fatal("sole-version deletion", deleted, err)
	}
	// The Skill and every encrypted version row are gone in the same commit.
	if skills, versions := skillRowCounts(t, pool, skill.ID); skills != 0 || versions != 0 {
		t.Fatal("orphaned Skill rows", skills, versions)
	}
	if _, err = s.GetSkill(t.Context(), tenant, skill.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted Skill is readable", err)
	}
	if _, err = s.ListSkillVersions(t.Context(), tenant, skill.ID, "", 20, false); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted Skill versions are listable", err)
	}
	if _, _, err = s.ReadDefaultSkillVersion(t.Context(), tenant, skill.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted Skill content is readable", err)
	}
	if page, err := s.ListSkills(t.Context(), tenant, "", 20, false); err != nil || len(page.Skills) != 0 {
		t.Fatal("deleted Skill is listed", page, err)
	}
	if _, err = s.DeleteSkillVersion(t.Context(), tenant, skill.ID, "1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("repeated sole-version deletion", err)
	}
	if err = s.DeleteSkill(t.Context(), tenant, skill.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("Skill deletion after sole-version deletion", err)
	}

	// Committed snapshots and Template intent are unchanged, as with DeleteSkill.
	after, err := s.ReadEnvironmentSetup(t.Context(), tenant, session.ID)
	if err != nil || !reflect.DeepEqual(after.Skills, frozen.Skills) {
		t.Fatal("frozen Session installation changed", err)
	}
	retry, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil || retry.ID != session.ID {
		t.Fatal("committed retry read the deleted source", err)
	}
	kept, err := s.GetEnvironmentTemplate(t.Context(), tenant, template.ID)
	if err != nil || !reflect.DeepEqual(kept.Skills, template.Skills) || !kept.UpdatedAt.Equal(template.UpdatedAt) {
		t.Fatal("Template reference intent changed", err)
	}
}

// Upload and sole-version deletion serialize on the Skill row: an upload that
// commits first makes the default undeletable, and a deletion that commits
// first makes the later upload miss the Skill. Neither loses acknowledged data.
func TestSoleSkillVersionDeletionSerializesWithUpload(t *testing.T) {
	s, pool := skillVersionDeletionStore(t)
	tenant := uuid.NewString()
	for _, uploadFirst := range []bool{true, false} {
		skill, err := s.CreateSkill(t.Context(), tenant, skillArchive(t, "race-first"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.DeleteSkill(t.Context(), tenant, skill.ID) })
		// Hold the owner lock so both operations queue in a known order.
		holder, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var holderPID int32
		if err = holder.QueryRow(t.Context(), "SELECT pg_backend_pid() FROM skills WHERE id=$1 FOR UPDATE", uuid.MustParse(skill.ID[len("skill_"):])).Scan(&holderPID); err != nil {
			t.Fatal(err)
		}
		type outcome struct {
			version SkillVersion
			err     error
		}
		uploaded, deleted := make(chan outcome, 1), make(chan outcome, 1)
		second := skillArchive(t, "race-second")
		upload := func() {
			version, err := s.CreateSkillVersion(t.Context(), tenant, skill.ID, second, false)
			uploaded <- outcome{version, err}
		}
		remove := func() {
			version, err := s.DeleteSkillVersion(t.Context(), tenant, skill.ID, "1")
			deleted <- outcome{version, err}
		}
		early, late := upload, remove
		if !uploadFirst {
			early, late = remove, upload
		}
		go early()
		waitForSkillLockWaiters(t, pool, holderPID, 1)
		go late()
		waitForSkillLockWaiters(t, pool, holderPID, 2)
		if err = holder.Rollback(t.Context()); err != nil {
			t.Fatal(err)
		}
		up, del := <-uploaded, <-deleted
		skills, versions := skillRowCounts(t, pool, skill.ID)
		if uploadFirst {
			if up.err != nil || up.version.Version != 2 || !errors.Is(del.err, ErrDefaultSkillVersion) || skills != 1 || versions != 2 {
				t.Fatal("upload before deletion", up, del, skills, versions)
			}
			current, err := s.GetSkill(t.Context(), tenant, skill.ID)
			if err != nil || current.DefaultVersion != 1 || current.LatestVersion != 2 {
				t.Fatal("pointers after serialized upload", current, err)
			}
		} else if del.err != nil || del.version.Version != 1 || !errors.Is(up.err, ErrNotFound) || skills != 0 || versions != 0 {
			t.Fatal("deletion before upload", up, del, skills, versions)
		}
	}
}

// waitForSkillLockWaiters waits until count sessions queue behind holder.
func waitForSkillLockWaiters(t *testing.T, pool *pgxpool.Pool, holder int32, count int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		err := pool.QueryRow(t.Context(), `WITH RECURSIVE queued(pid) AS (
 SELECT $1::int UNION SELECT a.pid FROM pg_stat_activity a JOIN queued q ON q.pid = ANY(pg_blocking_pids(a.pid))
) SELECT count(*) - 1 FROM queued`, holder).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting >= count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("lock waiters", waiting, count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
