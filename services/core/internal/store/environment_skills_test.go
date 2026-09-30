package store

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/google/uuid"
)

func TestSkillsFrozenInSession(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{17}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	header := &zip.FileHeader{Name: "proof/SKILL.md", Method: zip.Store}
	file, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte("---\nname: proof\ndescription: A proof.\n---\nprivate-skill-canary")); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	setup := environmentconfig.Setup{Skills: []environmentconfig.Skill{{Metadata: environmentconfig.SkillMetadata{Type: "inline", Name: "proof", Description: "A proof."}, Archive: archive.Bytes()}}}
	tenant, foreign := uuid.NewString(), uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`), Initialization: setup})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := s.ReadEnvironmentSetup(t.Context(), tenant, session.ID)
	if err != nil || !reflect.DeepEqual(frozen.Skills, setup.Skills) {
		t.Fatal("frozen content changed", err)
	}
	if _, err = s.ReadEnvironmentSetup(t.Context(), foreign, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign content", err)
	}
}
