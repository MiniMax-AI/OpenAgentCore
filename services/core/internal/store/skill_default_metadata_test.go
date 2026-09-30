package store

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/google/uuid"
)

func skillMetadataArchive(t *testing.T, name, description string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.CreateHeader(&zip.FileHeader{Name: name + "/SKILL.md", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintf(file, "---\nname: %s\ndescription: %s\n---\nPrivate marker for %s.\n", name, description, name); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestSkillMetadataTracksDefaultVersion(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{74}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	metadataOnly := New(pool)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	names := []string{"first-proof", "second-proof", "third-proof"}
	descriptions := []string{"First immutable version.", "Second immutable version.", "Third immutable version."}
	archives := make([][]byte, len(names))
	for i := range names {
		archives[i] = skillMetadataArchive(t, names[i], descriptions[i])
	}
	created, err := s.CreateSkill(t.Context(), tenant, archives[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteSkill(t.Context(), tenant, created.ID) })
	assertMetadata := func(value Skill, version, latest int64) {
		t.Helper()
		if value.ID != created.ID || !value.CreatedAt.Equal(created.CreatedAt) || value.DefaultVersion != version || value.LatestVersion != latest || value.Name != names[version-1] || value.Description != descriptions[version-1] {
			t.Fatalf("metadata does not track default %d/latest %d: %+v", version, latest, value)
		}
	}
	assertStored := func(version, latest int64) {
		t.Helper()
		value, err := metadataOnly.GetSkill(t.Context(), tenant, created.ID)
		if err != nil {
			t.Fatal("metadata read without content key", err)
		}
		assertMetadata(value, version, latest)
		page, err := metadataOnly.ListSkills(t.Context(), tenant, "", 10, true)
		if err != nil || len(page.Skills) != 1 {
			t.Fatal("metadata list without content key", page, err)
		}
		assertMetadata(page.Skills[0], version, latest)
		selected, body, err := s.ReadDefaultSkillVersion(t.Context(), tenant, created.ID)
		if err != nil || selected.Version != version || selected.Name != names[version-1] || selected.Description != descriptions[version-1] || !bytes.Equal(body, archives[version-1]) {
			t.Fatal("default content differs from public metadata", selected, err)
		}
	}
	assertMetadata(created, 1, 1)
	if _, err = s.CreateSkillVersion(t.Context(), tenant, created.ID, archives[1], false); err != nil {
		t.Fatal(err)
	}
	assertStored(1, 2)
	for _, version := range []int64{2, 1} {
		updated, err := metadataOnly.UpdateSkillDefault(t.Context(), tenant, created.ID, strconv.FormatInt(version, 10))
		if err != nil {
			t.Fatal("default update without content key", err)
		}
		assertMetadata(updated, version, 2)
		assertStored(version, 2)
	}
	if _, err = metadataOnly.UpdateSkillDefault(t.Context(), foreign, created.ID, "2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign default update", err)
	}
	if _, err = metadataOnly.UpdateSkillDefault(t.Context(), tenant, created.ID, "999"); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing default update", err)
	}
	assertStored(1, 2)
	if _, err = s.CreateSkillVersion(t.Context(), tenant, created.ID, archives[2], true); err != nil {
		t.Fatal(err)
	}
	assertStored(3, 3)
	for i := range archives {
		version, body, err := s.ReadSkillVersion(t.Context(), tenant, created.ID, strconv.Itoa(i+1))
		if err != nil || version.Name != names[i] || version.Description != descriptions[i] || !bytes.Equal(body, archives[i]) {
			t.Fatal("default changes modified immutable version", i+1, version, err)
		}
	}
}
