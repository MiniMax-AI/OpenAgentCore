package vaultpg_test

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

func TestMCPCredentialSelectionAndScopedDecryption(t *testing.T) {
	store, pool := openStore(t)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	service := newService(t, pool, newCipher(t, key), nil)
	keyless := newService(t, pool, nil, nil)
	var owned []vaults.Vault
	for _, owner := range []string{tenant, tenant, foreign} {
		owned = append(owned, createVault(t, service, owner))
	}
	token := " \t" + uuid.NewString() + "雪\n"
	destination := "https://mcp.example/tools"
	create := func(vault vaults.Vault) vaults.Credential {
		t.Helper()
		return createStatic(t, service, vault.TenantID, vault.ID, "private", destination, token)
	}
	first, foreignCredential := create(owned[0]), create(owned[2])
	attached := []string{owned[0].ID, owned[1].ID}
	requests := []vaults.MCPCredentialRequest{{ServerLabel: "tools", ServerURL: destination}, {ServerLabel: "anonymous", ServerURL: "https://anonymous.example/mcp"}}
	selectFor := func(service *vaults.Service, tenant string, attached []string, requests []vaults.MCPCredentialRequest) ([]vaults.MCPCredentialBinding, error) {
		return service.ResolveMCPCredentials(t.Context(), vaults.ResolveMCPCredentials{TenantID: tenant, VaultIDs: attached, Requests: requests})
	}
	// Selection reads metadata only, so a keyless Core can select.
	bindings, err := selectFor(keyless, tenant, attached, requests)
	if err != nil || len(bindings) != 2 || bindings[0].CredentialID != first.ID || bindings[0].AuthType != vaults.AuthStaticBearer || bindings[1].CredentialID != "" {
		t.Fatal("metadata selection or frozen anonymous decision differs", err)
	}
	encoded, err := json.Marshal(bindings)
	if err != nil || bytes.Contains(encoded, []byte(token)) || strings.Contains(string(encoded), "ciphertext") {
		t.Fatal("private binding contains secret material")
	}
	second := create(owned[1])
	if _, err := selectFor(keyless, tenant, attached, requests); !isSelectionError(err, true, "multiple attached vault credentials match MCP server_url "+destination+"; specify credential_id") {
		t.Fatal("ambiguous selection was admitted", err)
	}
	requests[0].CredentialID = &second.ID
	explicit, err := selectFor(keyless, tenant, attached, requests)
	if err != nil || explicit[0].CredentialID != second.ID || requests[1].CredentialID != nil {
		t.Fatal("explicit selection did not disambiguate", err)
	}
	notAttached := func(id string) string { return "MCP credential_id " + id + " was not found in an attached vault" }
	for _, tc := range []struct {
		owner   string
		vaults  []string
		id, url string
		message string // Empty for the unchanged Vault 404.
	}{
		{tenant, attached, foreignCredential.ID, destination, notAttached(foreignCredential.ID)},
		{tenant, []string{owned[1].ID}, first.ID, destination, notAttached(first.ID)},
		{tenant, attached, "not-a-credential", destination, notAttached("not-a-credential")},
		{tenant, attached, first.ID, destination + "/other", "MCP credential_id " + first.ID + " does not match server_url " + destination + "/other"},
		{foreign, attached, first.ID, destination, ""},
		{tenant, []string{owned[0].ID, owned[2].ID}, first.ID, destination, ""},
		{tenant, []string{uuid.NewString()}, first.ID, destination, ""},
	} {
		_, err := selectFor(keyless, tc.owner, tc.vaults, []vaults.MCPCredentialRequest{{ServerLabel: "tools", ServerURL: tc.url, CredentialID: &tc.id}})
		if tc.message == "" && !errors.Is(err, vaults.ErrNotFound) || tc.message != "" && !isSelectionError(err, false, tc.message) {
			t.Fatal("unowned, unattached or wrong-destination selection was admitted", err)
		}
	}
	if _, err := selectFor(keyless, tenant, nil, []vaults.MCPCredentialRequest{{ServerLabel: "tools", ServerURL: destination, CredentialID: &first.ID}}); !isSelectionError(err, false, "MCP credential_id requires an attached vault") {
		t.Fatal("a reference without attachments was admitted", err)
	}
	pool.Close()
	store, pool = openStore(t)
	service = newService(t, pool, newCipher(t, bytes.Clone(key)), nil)
	keyless = newService(t, pool, nil, nil)
	got, err := bearerToken(t.Context(), service, tenant, attached, bindings[0])
	if err != nil || got != token {
		t.Fatal("frozen selection or opaque bytes changed across restart", err)
	}
	if got, err := bearerToken(t.Context(), keyless, tenant, attached, bindings[0]); !errors.Is(err, credentialcrypto.ErrUnavailable) || got != "" {
		t.Fatal("missing key did not fail execution closed")
	}
	key[0] ^= 1
	if got, err := bearerToken(t.Context(), newService(t, pool, newCipher(t, key), nil), tenant, attached, bindings[0]); err == nil || got != "" || strings.Contains(err.Error(), token) {
		t.Fatal("wrong key leaked or decrypted a credential")
	}
	for _, mutate := range []func(*vaults.MCPCredentialBinding){
		func(b *vaults.MCPCredentialBinding) { b.VaultID = owned[1].ID },
		func(b *vaults.MCPCredentialBinding) { b.CredentialID = foreignCredential.ID },
		func(b *vaults.MCPCredentialBinding) { b.ServerURL += "/other" },
		func(b *vaults.MCPCredentialBinding) { b.AuthType = "other" },
	} {
		binding := bindings[0]
		mutate(&binding)
		if got, err := bearerToken(t.Context(), service, tenant, attached, binding); !errors.Is(err, vaults.ErrNotFound) || got != "" {
			t.Fatal("substituted frozen authorization was decrypted")
		}
	}
	for _, scope := range []struct {
		owner  string
		vaults []string
	}{{foreign, attached}, {tenant, []string{owned[1].ID}}} {
		if got, err := bearerToken(t.Context(), service, scope.owner, scope.vaults, bindings[0]); !errors.Is(err, vaults.ErrNotFound) || got != "" {
			t.Fatal("tenant or attachment authorization was bypassed")
		}
	}
	if _, err := pool.Exec(t.Context(), "UPDATE vault_credentials SET token_ciphertext=set_byte(token_ciphertext, 15, get_byte(token_ciphertext,15) # 1) WHERE id=$1", first.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := bearerToken(t.Context(), service, tenant, attached, bindings[0]); err == nil || got != "" {
		t.Fatal("tampered ciphertext decrypted")
	}
	if _, err := store.GetCredential(t.Context(), tenant, first.VaultID, first.ID); err != nil {
		t.Fatal("safe metadata lookup depended on ciphertext", err)
	}
}
