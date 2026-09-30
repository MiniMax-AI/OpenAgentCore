package skillpg_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/skillpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fixture is one Skill store and the service over it.
type fixture struct {
	pool    *pgxpool.Pool
	store   *skillpg.Store
	service *skills.Service
}

func newFixture(t *testing.T, pool *pgxpool.Pool, cipher *credentialcrypto.Cipher) fixture {
	t.Helper()
	store := skillpg.New(pgunit.NewPool(pool), cipher)
	service, err := skills.NewService(store, store)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{pool: pool, store: store, service: service}
}

func testCipher(t *testing.T, seed byte) *credentialcrypto.Cipher {
	t.Helper()
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{seed}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func archive(t *testing.T, name, description, marker string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.CreateHeader(&zip.FileHeader{Name: name + "/SKILL.md", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintf(file, "---\nname: %s\ndescription: %s\n---\n%s\n", name, description, marker); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func proofArchive(t *testing.T, marker string) []byte {
	return archive(t, "proof", "Verify a versioned Skill.", marker)
}

func key(t *testing.T, id string) uuid.UUID {
	t.Helper()
	value, err := skills.ParseID(id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func (f fixture) create(t *testing.T, tenant string, archive []byte) skills.Skill {
	t.Helper()
	created, err := f.service.CreateSkill(t.Context(), skills.CreateSkill{TenantID: tenant, Archive: archive})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.store.DeleteSkill(t.Context(), tenant, key(t, created.ID)) })
	return created
}

func (f fixture) rowCounts(t *testing.T, skill uuid.UUID) (skillRows, versionRows int) {
	t.Helper()
	if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM skills WHERE id=$1), (SELECT count(*) FROM skill_versions WHERE skill_id=$1)", skill).Scan(&skillRows, &versionRows); err != nil {
		t.Fatal(err)
	}
	return skillRows, versionRows
}

func TestOwnershipEncryptionAndVersions(t *testing.T) {
	f := newFixture(t, pgtest.Open(t), testCipher(t, 41))
	ctx := t.Context()
	tenant, foreign := uuid.NewString(), uuid.NewString()
	bundle := proofArchive(t, "confidential-skill-canary")
	created := f.create(t, tenant, bundle)
	id := key(t, created.ID)
	if created.DefaultVersion != 1 || created.LatestVersion != 1 {
		t.Fatal("initial pointers", created)
	}
	metadata, err := skillpg.New(pgunit.NewPool(f.pool), nil).Skill(ctx, tenant, id)
	if err != nil || metadata.Name != "proof" {
		t.Fatal("metadata requires no content key", err)
	}
	var contents []byte
	if err = f.pool.QueryRow(ctx, "SELECT contents FROM skill_versions WHERE tenant_id=$1", tenant).Scan(&contents); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(contents, []byte("confidential-skill-canary")) {
		t.Fatal("plaintext bundle persisted")
	}
	content, err := f.service.ReadVersion(ctx, skills.ReadVersion{TenantID: tenant, SkillID: id, Version: 1})
	if err != nil || !bytes.Equal(content.Archive, bundle) || content.Version.Version != 1 {
		t.Fatal("content round trip", err)
	}

	// Another tenant's Skill is a missing one for every operation.
	if _, err = f.store.Skill(ctx, foreign, id); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("foreign metadata", err)
	}
	if _, err = f.service.ReadVersion(ctx, skills.ReadVersion{TenantID: foreign, SkillID: id, Version: 1}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("foreign content", err)
	}
	if _, err = f.service.CreateVersion(ctx, skills.CreateVersion{TenantID: foreign, SkillID: id, Archive: bundle, MakeDefault: true}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("foreign version", err)
	}
	if _, err = f.service.SetDefaultVersion(ctx, skills.SetDefaultVersion{TenantID: foreign, SkillID: id, Version: "1"}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("foreign pointer", err)
	}
	if err = f.service.DeleteSkill(ctx, skills.DeleteSkill{TenantID: foreign, SkillID: id}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("foreign delete", err)
	}
	if _, err = f.service.ListSkills(ctx, skills.ListSkills{TenantID: foreign, After: created.ID, Limit: 20}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("foreign cursor", err)
	}
	if _, err = f.service.ListVersions(ctx, skills.ListVersions{TenantID: foreign, SkillID: id, Limit: 20}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("foreign versions", err)
	}

	// Concurrent uploads allocate consecutive version numbers.
	const count = 8
	results := make(chan skills.Version, count)
	failures := make(chan error, count)
	var group sync.WaitGroup
	for range count {
		group.Go(func() {
			version, err := f.service.CreateVersion(ctx, skills.CreateVersion{TenantID: tenant, SkillID: id, Archive: bundle})
			if err != nil {
				failures <- err
			} else {
				results <- version
			}
		})
	}
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	numbers := []int{}
	for version := range results {
		numbers = append(numbers, int(version.Version))
	}
	sort.Ints(numbers)
	for i, number := range numbers {
		if number != i+2 {
			t.Fatal("concurrent version allocation", numbers)
		}
	}
	if len(numbers) != count {
		t.Fatal("missing versions", numbers)
	}
	current, err := f.store.Skill(ctx, tenant, id)
	if err != nil || current.DefaultVersion != 1 || current.LatestVersion != count+1 {
		t.Fatal("concurrent pointers", current, err)
	}
	first, err := f.service.ListVersions(ctx, skills.ListVersions{TenantID: tenant, SkillID: id, Limit: 3, Ascending: true})
	if err != nil || !first.HasMore || len(first.Versions) != 3 || first.Versions[0].Version != 1 {
		t.Fatal("first page", first, err)
	}
	next, err := f.service.ListVersions(ctx, skills.ListVersions{TenantID: tenant, SkillID: id, After: first.Versions[2].ID, Limit: 20, Ascending: true})
	if err != nil || next.HasMore || len(next.Versions) != 6 || next.Versions[0].Version != 4 {
		t.Fatal("version resource cursor", next, err)
	}
	var cursorErr *skills.CursorError
	if _, err = f.service.ListVersions(ctx, skills.ListVersions{TenantID: tenant, SkillID: id, After: "3", Limit: 20, Ascending: true}); !errors.As(err, &cursorErr) || cursorErr.Message != "Invalid 'after': '3'. Expected an ID that begins with 'skillver'." {
		t.Fatal("numeric version is not a cursor", err)
	}
	if _, err = f.service.SetDefaultVersion(ctx, skills.SetDefaultVersion{TenantID: tenant, SkillID: id, Version: "999"}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("missing default", err)
	}
	updated, err := f.service.SetDefaultVersion(ctx, skills.SetDefaultVersion{TenantID: tenant, SkillID: id, Version: "3"})
	if err != nil || updated.DefaultVersion != 3 {
		t.Fatal("default update", err)
	}
	if _, err = f.service.DeleteVersion(ctx, skills.DeleteVersion{TenantID: tenant, SkillID: id, Version: 3}); !errors.Is(err, skills.ErrDefaultVersion) {
		t.Fatal("default deletion", err)
	}
	if _, err = f.service.DeleteVersion(ctx, skills.DeleteVersion{TenantID: tenant, SkillID: id, Version: count + 1}); err != nil {
		t.Fatal(err)
	}
	if current, err = f.store.Skill(ctx, tenant, id); err != nil || current.LatestVersion != count {
		t.Fatal("latest pointer after deleting the latest version", current, err)
	}
	added, err := f.service.CreateVersion(ctx, skills.CreateVersion{TenantID: tenant, SkillID: id, Archive: bundle, MakeDefault: true})
	if err != nil || added.Version != count+2 {
		t.Fatal("deleted version number reused", added, err)
	}
	if err = f.service.DeleteSkill(ctx, skills.DeleteSkill{TenantID: tenant, SkillID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.ReadVersion(ctx, skills.ReadVersion{TenantID: tenant, SkillID: id, Version: 1}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("cascaded content", err)
	}
	var remaining int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM skill_versions WHERE tenant_id=$1", tenant).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("orphan content", remaining, err)
	}
}

// A zero page is empty; HasMore reports whether a resource follows the cursor.
func TestListsAcceptLimitZero(t *testing.T) {
	f := newFixture(t, pgtest.Open(t), testCipher(t, 43))
	ctx := t.Context()
	tenant, foreign := uuid.NewString(), uuid.NewString()
	bundle := proofArchive(t, "limit-zero")
	created := f.create(t, tenant, bundle)
	id := key(t, created.ID)
	if _, err := f.service.CreateVersion(ctx, skills.CreateVersion{TenantID: tenant, SkillID: id, Archive: bundle}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		tenant, after string
		hasMore       bool
	}{{tenant, "", true}, {tenant, created.ID, false}, {foreign, "", false}} {
		page, err := f.service.ListSkills(ctx, skills.ListSkills{TenantID: test.tenant, After: test.after, Limit: 0, Ascending: true})
		if err != nil || len(page.Skills) != 0 || page.HasMore != test.hasMore {
			t.Fatalf("zero Skill page after %q: %+v %v", test.after, page, err)
		}
	}
	versions, err := f.service.ListVersions(ctx, skills.ListVersions{TenantID: tenant, SkillID: id, Limit: 100, Ascending: true})
	if err != nil || len(versions.Versions) != 2 {
		t.Fatal("versions", versions, err)
	}
	for after, hasMore := range map[string]bool{"": true, versions.Versions[0].ID: true, versions.Versions[1].ID: false} {
		page, err := f.service.ListVersions(ctx, skills.ListVersions{TenantID: tenant, SkillID: id, After: after, Limit: 0, Ascending: true})
		if err != nil || len(page.Versions) != 0 || page.HasMore != hasMore {
			t.Fatalf("zero version page after %q: %+v %v", after, page, err)
		}
	}
	// Foreign cursors and parents stay indistinguishable from missing ones.
	if _, err = f.service.ListSkills(ctx, skills.ListSkills{TenantID: foreign, After: created.ID, Ascending: true}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("foreign cursor", err)
	}
	if _, err = f.service.ListVersions(ctx, skills.ListVersions{TenantID: foreign, SkillID: id, Ascending: true}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("foreign versions", err)
	}
	// A version cursor of another Skill in the tenant is a cursor error; one in
	// another tenant is missing.
	other := f.create(t, tenant, bundle)
	otherPage, err := f.service.ListVersions(ctx, skills.ListVersions{TenantID: tenant, SkillID: key(t, other.ID), Limit: 1})
	if err != nil || len(otherPage.Versions) != 1 {
		t.Fatal("other Skill versions", otherPage, err)
	}
	var cursorErr *skills.CursorError
	if _, err = f.service.ListVersions(ctx, skills.ListVersions{TenantID: tenant, SkillID: id, After: otherPage.Versions[0].ID, Limit: 1}); !errors.As(err, &cursorErr) || cursorErr.Message != "Skill version cursor does not match this skill." {
		t.Fatal("other Skill's cursor", err)
	}
	foreignSkill := f.create(t, foreign, bundle)
	foreignPage, err := f.service.ListVersions(ctx, skills.ListVersions{TenantID: foreign, SkillID: key(t, foreignSkill.ID), Limit: 1})
	if err != nil || len(foreignPage.Versions) != 1 {
		t.Fatal("foreign Skill versions", foreignPage, err)
	}
	if _, err = f.service.ListVersions(ctx, skills.ListVersions{TenantID: tenant, SkillID: id, After: foreignPage.Versions[0].ID, Limit: 1}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("foreign version cursor", err)
	}
}

func TestMetadataTracksDefaultVersion(t *testing.T) {
	pool := pgtest.Open(t)
	f := newFixture(t, pool, testCipher(t, 74))
	// Metadata reads and default changes need no content key.
	metadataOnly := newFixture(t, pool, nil)
	ctx := t.Context()
	tenant, foreign := uuid.NewString(), uuid.NewString()
	names := []string{"first-proof", "second-proof", "third-proof"}
	descriptions := []string{"First immutable version.", "Second immutable version.", "Third immutable version."}
	archives := make([][]byte, len(names))
	for i := range names {
		archives[i] = archive(t, names[i], descriptions[i], "Private marker for "+names[i]+".")
	}
	created := f.create(t, tenant, archives[0])
	id := key(t, created.ID)
	assertMetadata := func(value skills.Skill, version, latest int64) {
		t.Helper()
		if value.ID != created.ID || !value.CreatedAt.Equal(created.CreatedAt) || value.DefaultVersion != version || value.LatestVersion != latest || value.Name != names[version-1] || value.Description != descriptions[version-1] {
			t.Fatalf("metadata does not track default %d/latest %d: %+v", version, latest, value)
		}
	}
	assertStored := func(version, latest int64) {
		t.Helper()
		value, err := metadataOnly.store.Skill(ctx, tenant, id)
		if err != nil {
			t.Fatal("metadata read without content key", err)
		}
		assertMetadata(value, version, latest)
		page, err := metadataOnly.service.ListSkills(ctx, skills.ListSkills{TenantID: tenant, Limit: 10, Ascending: true})
		if err != nil || len(page.Skills) != 1 {
			t.Fatal("metadata list without content key", page, err)
		}
		assertMetadata(page.Skills[0], version, latest)
		selected, err := f.service.ReadDefaultVersion(ctx, skills.ReadDefaultVersion{TenantID: tenant, SkillID: id})
		if err != nil || selected.Version.Version != version || selected.Version.Name != names[version-1] || selected.Version.Description != descriptions[version-1] || !bytes.Equal(selected.Archive, archives[version-1]) {
			t.Fatal("default content differs from public metadata", selected.Version, err)
		}
	}
	assertMetadata(created, 1, 1)
	if _, err := f.service.CreateVersion(ctx, skills.CreateVersion{TenantID: tenant, SkillID: id, Archive: archives[1]}); err != nil {
		t.Fatal(err)
	}
	assertStored(1, 2)
	for _, version := range []string{"2", "1"} {
		updated, err := metadataOnly.service.SetDefaultVersion(ctx, skills.SetDefaultVersion{TenantID: tenant, SkillID: id, Version: version})
		if err != nil {
			t.Fatal("default update without content key", err)
		}
		assertMetadata(updated, updated.DefaultVersion, 2)
		assertStored(updated.DefaultVersion, 2)
	}
	if _, err := metadataOnly.service.SetDefaultVersion(ctx, skills.SetDefaultVersion{TenantID: foreign, SkillID: id, Version: "2"}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("foreign default update", err)
	}
	if _, err := metadataOnly.service.SetDefaultVersion(ctx, skills.SetDefaultVersion{TenantID: tenant, SkillID: id, Version: "999"}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("missing default update", err)
	}
	assertStored(1, 2)
	// Uploads and content reads need the key; the failed upload stores nothing.
	if _, err := metadataOnly.service.CreateVersion(ctx, skills.CreateVersion{TenantID: tenant, SkillID: id, Archive: archives[2]}); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("upload without content key", err)
	}
	if _, err := metadataOnly.service.ReadDefaultVersion(ctx, skills.ReadDefaultVersion{TenantID: tenant, SkillID: id}); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("content read without content key", err)
	}
	assertStored(1, 2)
	if _, err := f.service.CreateVersion(ctx, skills.CreateVersion{TenantID: tenant, SkillID: id, Archive: archives[2], MakeDefault: true}); err != nil {
		t.Fatal(err)
	}
	assertStored(3, 3)
	for i := range archives {
		content, err := f.service.ReadVersion(ctx, skills.ReadVersion{TenantID: tenant, SkillID: id, Version: int64(i + 1)})
		if err != nil || content.Version.Name != names[i] || content.Version.Description != descriptions[i] || !bytes.Equal(content.Archive, archives[i]) {
			t.Fatal("default changes modified immutable version", i+1, content.Version, err)
		}
	}
}

// Deleting the sole version deletes the Skill and all its rows in one commit.
func TestSoleVersionDeletionRemovesSkill(t *testing.T) {
	f := newFixture(t, pgtest.Open(t), testCipher(t, 63))
	ctx := t.Context()
	tenant, foreign := uuid.NewString(), uuid.NewString()
	skill := f.create(t, tenant, proofArchive(t, "sole-version"))
	id := key(t, skill.ID)
	version, err := f.store.Version(ctx, tenant, id, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Foreign and missing targets fail before any mutation.
	if _, err = f.service.DeleteVersion(ctx, skills.DeleteVersion{TenantID: foreign, SkillID: id, Version: 1}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("foreign sole-version deletion", err)
	}
	if _, err = f.service.DeleteVersion(ctx, skills.DeleteVersion{TenantID: tenant, SkillID: id, Version: 2}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("missing version deletion", err)
	}
	if skillRows, versionRows := f.rowCounts(t, id); skillRows != 1 || versionRows != 1 {
		t.Fatal("rejected deletion changed rows", skillRows, versionRows)
	}
	deleted, err := f.service.DeleteVersion(ctx, skills.DeleteVersion{TenantID: tenant, SkillID: id, Version: 1})
	if err != nil || deleted.ID != version.ID || deleted.SkillID != skill.ID || deleted.Version != 1 {
		t.Fatal("sole-version deletion", deleted, err)
	}
	if skillRows, versionRows := f.rowCounts(t, id); skillRows != 0 || versionRows != 0 {
		t.Fatal("orphaned Skill rows", skillRows, versionRows)
	}
	if _, err = f.store.Skill(ctx, tenant, id); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("deleted Skill is readable", err)
	}
	if _, err = f.service.ListVersions(ctx, skills.ListVersions{TenantID: tenant, SkillID: id, Limit: 20}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("deleted Skill versions are listable", err)
	}
	if _, err = f.service.ReadDefaultVersion(ctx, skills.ReadDefaultVersion{TenantID: tenant, SkillID: id}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("deleted Skill content is readable", err)
	}
	if page, err := f.service.ListSkills(ctx, skills.ListSkills{TenantID: tenant, Limit: 20}); err != nil || len(page.Skills) != 0 {
		t.Fatal("deleted Skill is listed", page, err)
	}
	if _, err = f.service.DeleteVersion(ctx, skills.DeleteVersion{TenantID: tenant, SkillID: id, Version: 1}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("repeated sole-version deletion", err)
	}
	if err = f.service.DeleteSkill(ctx, skills.DeleteSkill{TenantID: tenant, SkillID: id}); !errors.Is(err, skills.ErrNotFound) {
		t.Fatal("Skill deletion after sole-version deletion", err)
	}
}

// Upload and sole-version deletion serialize on the Skill row: an upload that
// commits first makes the default undeletable, and a deletion that commits
// first makes the later upload miss the Skill. Neither loses acknowledged data.
func TestSoleVersionDeletionSerializesWithUpload(t *testing.T) {
	f := newFixture(t, pgtest.Open(t), testCipher(t, 63))
	ctx := t.Context()
	tenant := uuid.NewString()
	for _, uploadFirst := range []bool{true, false} {
		skill := f.create(t, tenant, proofArchive(t, "race-first"))
		id := key(t, skill.ID)
		// Hold the owner lock so both operations queue in a known order.
		holder, err := f.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var holderPID int32
		if err = holder.QueryRow(ctx, "SELECT pg_backend_pid() FROM skills WHERE id=$1 FOR UPDATE", id).Scan(&holderPID); err != nil {
			t.Fatal(err)
		}
		type outcome struct {
			version skills.Version
			err     error
		}
		uploaded, deleted := make(chan outcome, 1), make(chan outcome, 1)
		second := proofArchive(t, "race-second")
		upload := func() {
			version, err := f.service.CreateVersion(ctx, skills.CreateVersion{TenantID: tenant, SkillID: id, Archive: second})
			uploaded <- outcome{version, err}
		}
		remove := func() {
			version, err := f.service.DeleteVersion(ctx, skills.DeleteVersion{TenantID: tenant, SkillID: id, Version: 1})
			deleted <- outcome{version, err}
		}
		early, late := upload, remove
		if !uploadFirst {
			early, late = remove, upload
		}
		go early()
		waitForLockWaiters(t, f.pool, holderPID, 1)
		go late()
		waitForLockWaiters(t, f.pool, holderPID, 2)
		if err = holder.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		up, del := <-uploaded, <-deleted
		skillRows, versionRows := f.rowCounts(t, id)
		if uploadFirst {
			if up.err != nil || up.version.Version != 2 || !errors.Is(del.err, skills.ErrDefaultVersion) || skillRows != 1 || versionRows != 2 {
				t.Fatal("upload before deletion", up, del, skillRows, versionRows)
			}
			current, err := f.store.Skill(ctx, tenant, id)
			if err != nil || current.DefaultVersion != 1 || current.LatestVersion != 2 {
				t.Fatal("pointers after serialized upload", current, err)
			}
		} else if del.err != nil || del.version.Version != 1 || !errors.Is(up.err, skills.ErrNotFound) || skillRows != 0 || versionRows != 0 {
			t.Fatal("deletion before upload", up, del, skillRows, versionRows)
		}
	}
}

// waitForLockWaiters waits until count sessions queue behind holder.
func waitForLockWaiters(t *testing.T, pool *pgxpool.Pool, holder int32, count int) {
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

// Text PostgreSQL cannot store, here a YAML-escaped U+0000 in the manifest
// description, is the shared unstorable-text error and stores nothing.
func TestUnstorableTextStoresNothing(t *testing.T) {
	f := newFixture(t, pgtest.Open(t), testCipher(t, 45))
	ctx := t.Context()
	tenant := uuid.NewString()
	bad := archive(t, "proof", `"before\0after"`, "unstorable")
	if _, err := f.service.CreateSkill(ctx, skills.CreateSkill{TenantID: tenant, Archive: bad}); !errors.Is(err, textvalue.ErrUnstorable) {
		t.Fatal("create", err)
	}
	created := f.create(t, tenant, proofArchive(t, "storable"))
	if _, err := f.service.CreateVersion(ctx, skills.CreateVersion{TenantID: tenant, SkillID: key(t, created.ID), Archive: bad, MakeDefault: true}); !errors.Is(err, textvalue.ErrUnstorable) {
		t.Fatal("version", err)
	}
	var skillRows, versionRows int
	if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM skills WHERE tenant_id=$1), (SELECT count(*) FROM skill_versions WHERE tenant_id=$1)", tenant).Scan(&skillRows, &versionRows); err != nil || skillRows != 1 || versionRows != 1 {
		t.Fatal("rejected text left rows", skillRows, versionRows, err)
	}
	if current, err := f.store.Skill(ctx, tenant, key(t, created.ID)); err != nil || current.LatestVersion != 1 || current.DefaultVersion != 1 {
		t.Fatal("rejected version moved the pointers", current, err)
	}
}

// A malformed tenant is invalid input, not a database error.
func TestMalformedTenantIsInvalidInput(t *testing.T) {
	f := newFixture(t, pgtest.Open(t), testCipher(t, 46))
	if _, err := f.service.CreateSkill(t.Context(), skills.CreateSkill{TenantID: "not-a-tenant", Archive: proofArchive(t, "tenant")}); !errors.Is(err, skills.ErrInvalidInput) {
		t.Fatal("create", err)
	}
	if _, err := f.store.Skill(t.Context(), "not-a-tenant", uuid.New()); !errors.Is(err, skills.ErrInvalidInput) {
		t.Fatal("read", err)
	}
}
