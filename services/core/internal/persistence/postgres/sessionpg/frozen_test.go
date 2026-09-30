package sessionpg

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func frozenCipher(t *testing.T) *credentialcrypto.Cipher {
	t.Helper()
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

// corruptFrozenData reports whether err classifies frozen data as corrupt
// stored data: an internal error, never the caller's input, a missing record
// or a missing key.
func corruptFrozenData(err error) bool {
	return err != nil && !errors.Is(err, sessions.ErrInvalidInput) && !errors.Is(err, sessions.ErrNotFound) && !errors.Is(err, credentialcrypto.ErrUnavailable)
}

func TestReadEnvironmentSetupClassifiesFrozenData(t *testing.T) {
	pool := pgtest.Open(t)
	cipher := frozenCipher(t)
	adapter := New(pgunit.NewPool(pool), cipher)
	tenantID, sessionID, _ := newEnvironment(t, pool, "self_hosted", "pending")
	tenant, session := uuidText(tenantID), uuidText(sessionID)
	binding := credentialcrypto.EnvironmentSetupBinding{TenantID: tenant, Resource: "session", OwnerID: session, Field: "initialization"}
	freeze := func(plaintext string, binding credentialcrypto.EnvironmentSetupBinding) []byte {
		t.Helper()
		sealed, err := cipher.SealEnvironmentSetup([]byte(plaintext), binding)
		if err != nil {
			t.Fatal(err)
		}
		return sealed
	}

	if setup, err := adapter.ReadEnvironmentSetup(t.Context(), tenant, session); err != nil || !reflect.DeepEqual(setup, environmentconfig.Setup{}) {
		t.Fatalf("Session without a frozen setup: %+v, %v", setup, err)
	}
	for name, ids := range map[string][2]string{"unknown Session": {tenant, uuid.NewString()}, "other tenant": {uuid.NewString(), session}, "malformed Session": {tenant, "session"}} {
		if _, err := adapter.ReadEnvironmentSetup(t.Context(), ids[0], ids[1]); !errors.Is(err, sessions.ErrNotFound) {
			t.Fatalf("%s: %v", name, err)
		}
	}

	exec(t, pool, `INSERT INTO environment_setups(session_id, contents) VALUES ($1, $2)`, sessionID, freeze(`{"env":{"GREETING":"hello"},"packages":{"npm":["left-pad"],"python":[]}}`, binding))
	setup, err := adapter.ReadEnvironmentSetup(t.Context(), tenant, session)
	if err != nil || setup.Env["GREETING"] != "hello" || !reflect.DeepEqual(setup.Packages.NPM, []string{"left-pad"}) {
		t.Fatalf("frozen setup %+v, %v", setup, err)
	}
	if _, err := New(pgunit.NewPool(pool), nil).ReadEnvironmentSetup(t.Context(), tenant, session); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatalf("without a credential key: %v", err)
	}

	otherSession := binding
	otherSession.OwnerID = uuid.NewString()
	for name, contents := range map[string][]byte{
		"sealed for another Session": freeze(`{"packages":{"npm":[],"python":[]}}`, otherSession),
		"not ciphertext":             []byte("plaintext"),
		"not JSON":                   freeze(`packages`, binding),
		"unknown field":              freeze(`{"packages":{"npm":[],"python":[]},"network":{}}`, binding),
		"invalid setup":              freeze(`{"env":{"PATH":"/bin"},"packages":{"npm":[],"python":[]}}`, binding),
		"null system packages":       freeze(`{"packages":{"npm":[],"python":[],"system":null}}`, binding),
		"empty system packages":      freeze(`{"packages":{"npm":[],"python":[],"system":[]}}`, binding),
		"system packages":            freeze(`{"packages":{"npm":[],"python":[],"system":["jq"]}}`, binding),
	} {
		exec(t, pool, `UPDATE environment_setups SET contents = $2 WHERE session_id = $1`, sessionID, contents)
		if _, err := adapter.ReadEnvironmentSetup(t.Context(), tenant, session); !corruptFrozenData(err) {
			t.Fatalf("%s: %v", name, err)
		}
	}

	exec(t, pool, `UPDATE sessions SET deleted_at = clock_timestamp() WHERE id = $1`, sessionID)
	if _, err := adapter.ReadEnvironmentSetup(t.Context(), tenant, session); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("deleted Session: %v", err)
	}
}

func TestReadInitialEnvironmentFileClassifiesFrozenData(t *testing.T) {
	pool := pgtest.Open(t)
	cipher := frozenCipher(t)
	adapter := New(pgunit.NewPool(pool), cipher)
	tenantID, sessionID, _ := newEnvironment(t, pool, "self_hosted", "pending")
	tenant, session := uuidText(tenantID), uuidText(sessionID)
	file := uuid.NewString()
	binding := credentialcrypto.EnvironmentFileBinding{TenantID: tenant, Resource: "session", OwnerID: session, FileID: file}
	freeze := func(body string, binding credentialcrypto.EnvironmentFileBinding) []byte {
		t.Helper()
		sealed, err := cipher.SealEnvironmentFile([]byte(body), binding)
		if err != nil {
			t.Fatal(err)
		}
		return sealed
	}
	exec(t, pool, `INSERT INTO initial_environment_files(id, session_id, position, path, size_bytes, contents) VALUES ($1, $2, 0, 'notes.txt', 5, $3)`, file, sessionID, freeze("hello", binding))

	metadata, body, err := adapter.ReadInitialEnvironmentFile(t.Context(), tenant, session, 0)
	if err != nil || metadata.ID != file || metadata.Path != "notes.txt" || metadata.SizeBytes == nil || *metadata.SizeBytes != 5 || string(body) != "hello" {
		t.Fatalf("frozen file %+v %q, %v", metadata, body, err)
	}
	if _, _, err := adapter.ReadInitialEnvironmentFile(t.Context(), tenant, session, 1); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("missing position: %v", err)
	}
	if _, _, err := adapter.ReadInitialEnvironmentFile(t.Context(), uuid.NewString(), session, 0); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("other tenant: %v", err)
	}
	if _, _, err := New(pgunit.NewPool(pool), nil).ReadInitialEnvironmentFile(t.Context(), tenant, session, 0); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatalf("without a credential key: %v", err)
	}

	otherFile := binding
	otherFile.FileID = uuid.NewString()
	for name, contents := range map[string][]byte{
		"sealed for another file": freeze("hello", otherFile),
		"not ciphertext":          []byte("hello"),
		"size mismatch":           freeze("hello!", binding),
	} {
		exec(t, pool, `UPDATE initial_environment_files SET contents = $2 WHERE id = $1`, file, contents)
		if _, _, err := adapter.ReadInitialEnvironmentFile(t.Context(), tenant, session, 0); !corruptFrozenData(err) {
			t.Fatalf("%s: %v", name, err)
		}
	}

	exec(t, pool, `UPDATE sessions SET deleted_at = clock_timestamp() WHERE id = $1`, sessionID)
	if _, _, err := adapter.ReadInitialEnvironmentFile(t.Context(), tenant, session, 0); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("deleted Session: %v", err)
	}
}
