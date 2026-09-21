package store

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/google/uuid"
)

func skillArchive(t *testing.T, marker string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.CreateHeader(&zip.FileHeader{Name: "proof/SKILL.md", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintf(file, "---\nname: proof\ndescription: Verify a versioned Skill.\n---\n%s", marker); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestSkillsOwnershipEncryptionAndVersions(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{41}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	archive := skillArchive(t, "confidential-skill-canary")
	created, err := s.CreateSkill(t.Context(), tenant, archive)
	if err != nil {
		t.Fatal(err)
	}
	if created.DefaultVersion != 1 || created.LatestVersion != 1 {
		t.Fatal("initial pointers", created)
	}
	t.Cleanup(func() { _ = s.DeleteSkill(t.Context(), tenant, created.ID) })
	metadata, err := New(pool).GetSkill(t.Context(), tenant, created.ID)
	if err != nil || metadata.Name != "proof" {
		t.Fatal("metadata requires no content key", err)
	}
	var contents []byte
	if err = pool.QueryRow(t.Context(), "SELECT contents FROM skill_versions WHERE tenant_id=$1", tenant).Scan(&contents); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(contents, []byte("confidential-skill-canary")) {
		t.Fatal("plaintext bundle persisted")
	}
	version, body, err := s.ReadSkillVersion(t.Context(), tenant, created.ID, "1")
	if err != nil || !bytes.Equal(body, archive) || version.Version != 1 {
		t.Fatal("content round trip", err)
	}
	if _, err = s.GetSkill(t.Context(), foreign, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign metadata", err)
	}
	if _, _, err = s.ReadSkillVersion(t.Context(), foreign, created.ID, "1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign content", err)
	}
	if _, err = s.CreateSkillVersion(t.Context(), foreign, created.ID, archive, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign version", err)
	}
	if _, err = s.UpdateSkillDefault(t.Context(), foreign, created.ID, "1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign pointer", err)
	}
	if err = s.DeleteSkill(t.Context(), foreign, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign delete", err)
	}
	if _, err = s.ListSkills(t.Context(), foreign, created.ID, 20, false); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign cursor", err)
	}
	if _, err = s.ListSkillVersions(t.Context(), foreign, created.ID, "", 20, false); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign versions", err)
	}

	const count = 8
	results := make(chan SkillVersion, count)
	failures := make(chan error, count)
	var group sync.WaitGroup
	for range count {
		group.Add(1)
		go func() {
			defer group.Done()
			version, err := s.CreateSkillVersion(t.Context(), tenant, created.ID, archive, false)
			if err != nil {
				failures <- err
			} else {
				results <- version
			}
		}()
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
	current, err := s.GetSkill(t.Context(), tenant, created.ID)
	if err != nil || current.DefaultVersion != 1 || current.LatestVersion != count+1 {
		t.Fatal("concurrent pointers", current, err)
	}
	first, err := s.ListSkillVersions(t.Context(), tenant, created.ID, "", 3, true)
	if err != nil || !first.HasMore || len(first.Versions) != 3 || first.Versions[0].Version != 1 {
		t.Fatal("first page", first, err)
	}
	next, err := s.ListSkillVersions(t.Context(), tenant, created.ID, first.Versions[2].ID, 20, true)
	if err != nil || next.HasMore || len(next.Versions) != 6 || next.Versions[0].Version != 4 {
		t.Fatal("version resource cursor", next, err)
	}
	if _, err = s.ListSkillVersions(t.Context(), tenant, created.ID, "3", 20, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("numeric version is not a cursor", err)
	}
	if _, err = s.UpdateSkillDefault(t.Context(), tenant, created.ID, "999"); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing default", err)
	}
	updated, err := s.UpdateSkillDefault(t.Context(), tenant, created.ID, "3")
	if err != nil || updated.DefaultVersion != 3 {
		t.Fatal("default update", err)
	}
	if _, err = s.DeleteSkillVersion(t.Context(), tenant, created.ID, "3"); !errors.Is(err, ErrDefaultSkillVersion) {
		t.Fatal("default deletion", err)
	}
	if _, err = s.DeleteSkillVersion(t.Context(), tenant, created.ID, strconv.Itoa(count+1)); err != nil {
		t.Fatal(err)
	}
	added, err := s.CreateSkillVersion(t.Context(), tenant, created.ID, archive, true)
	if err != nil || added.Version != count+2 {
		t.Fatal("deleted version number reused", added, err)
	}
	if err = s.DeleteSkill(t.Context(), tenant, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ReadSkillVersion(t.Context(), tenant, created.ID, "1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("cascaded content", err)
	}
	var remaining int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM skill_versions WHERE tenant_id=$1", tenant).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("orphan content", remaining, err)
	}
}
