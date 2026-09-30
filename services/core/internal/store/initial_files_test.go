package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/filepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testFiles returns the files domain over pool for tests that need a File.
func testFiles(t *testing.T, pool *pgxpool.Pool) (*files.Service, *filepg.Store) {
	t.Helper()
	storage := filepg.New(pgunit.NewPool(pool))
	service, err := files.NewService(storage)
	if err != nil {
		t.Fatal(err)
	}
	return service, storage
}

func uploadSource(data []byte) func(io.Writer) (files.Upload, error) {
	return func(w io.Writer) (files.Upload, error) {
		_, err := w.Write(data)
		return files.Upload{Filename: "source.bin", Purpose: files.PurposeUserData}, err
	}
}

func TestInitialFilesFrozenEncryptedIsolatedAndRetryable(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	canary := []byte("private-initial-file-canary\x00\xff")
	sourceFiles, _ := testFiles(t, pool)
	upload, err := sourceFiles.Create(t.Context(), files.CreateCommand{TenantID: tenant, Upload: uploadSource(canary)})
	if err != nil {
		t.Fatal(err)
	}
	initial := []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/a/data", Data: canary}, {Type: "file_id", Path: "/workspace/b", FileID: upload.ID}}
	input := sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`), InitialFiles: initial}
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(session.Configuration, canary) || bytes.Contains(session.Configuration, []byte(`"data"`)) {
		t.Fatal("plaintext in Session configuration")
	}
	if err := sourceFiles.Delete(t.Context(), files.DeleteCommand{TenantID: tenant, FileID: upload.ID}); err != nil {
		t.Fatal(err)
	}
	retry, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil || retry.ID != session.ID {
		t.Fatal("retry re-resolved deleted resources", err)
	}
	for position := range initial {
		metadata, body, err := sessionAdapter(s).ReadInitialEnvironmentFile(t.Context(), strings.ToUpper(tenant), strings.ToUpper(session.ID), position)
		if err != nil || !bytes.Equal(body, canary) || metadata.ID == "" {
			t.Fatal("frozen initial content", err)
		}
		if _, _, err := sessionAdapter(s).ReadInitialEnvironmentFile(t.Context(), foreign, session.ID, position); err == nil {
			t.Fatal("foreign bytes disclosed")
		}
		var encrypted []byte
		if err := pool.QueryRow(t.Context(), "SELECT contents FROM initial_environment_files WHERE id=$1", metadata.ID).Scan(&encrypted); err != nil || bytes.Contains(encrypted, canary) {
			t.Fatal("unencrypted file storage", err)
		}
	}
	changed := input
	changed.InitialFiles = append([]environmentconfig.InitialFile(nil), initial...)
	changed.InitialFiles[0].Data = []byte("changed")
	if _, err := s.CreateSession(t.Context(), tenant, changed); !errors.Is(err, sessions.ErrIdempotencyConflict) {
		t.Fatal("changed bytes retried", err)
	}
	if _, err := sessionAdapter(s).GetSessionDevice(t.Context(), tenant, session.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("uninitialized environment exposed", err)
	}
}
