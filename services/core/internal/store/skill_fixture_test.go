package store

import (
	"archive/zip"
	"bytes"
	"fmt"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/skillpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/jackc/pgx/v5/pgxpool"
)

// skillArchive builds a minimal valid Skill archive.
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

// SkillService manages Skills in the database of a Store test. cipher must be
// the Store's, so Session creation can open the versions it freezes.
func SkillService(t testing.TB, pool *pgxpool.Pool, cipher *credentialcrypto.Cipher) *skills.Service {
	t.Helper()
	skillStore := skillpg.New(pgunit.NewPool(pool), cipher)
	service, err := skills.NewService(skillStore, skillStore)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
