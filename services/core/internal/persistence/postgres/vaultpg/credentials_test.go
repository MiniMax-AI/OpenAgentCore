package vaultpg_test

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

func TestStaticCredentialsPersistEncryptedAndRemainScoped(t *testing.T) {
	store, pool := openStore(t)
	ctx := t.Context()
	tenant, foreignTenant := uuid.NewString(), uuid.NewString()
	keyless := newService(t, pool, nil, nil)
	var owned []vaults.Vault
	for _, owner := range []string{tenant, tenant, foreignTenant} {
		owned = append(owned, createVault(t, keyless, owner))
	}
	key, randomToken := make([]byte, 32), make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(randomToken); err != nil {
		t.Fatal(err)
	}
	service := newService(t, pool, newCipher(t, key), nil)
	canary := hex.EncodeToString(randomToken)
	opaque := " \t" + canary + " 凭据\n" + strings.Repeat("x", 300) + " "
	tokens := []string{opaque, opaque, ""}
	var records []vaults.Credential
	before := time.Now().Add(-time.Second)
	for _, token := range tokens {
		command := vaults.CreateStaticCredential{TenantID: tenant, VaultID: owned[0].ID, Name: "MCP credential", MCPServerURL: "https://mcp.example/tools", Token: token}
		record, err := service.CreateStaticCredential(ctx, command)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := uuid.Parse(record.ID); err != nil || record.VaultID != owned[0].ID || record.Name != command.Name || record.AuthType != vaults.AuthStaticBearer || record.MCPServerURL != command.MCPServerURL || record.CreatedAt.Before(before) || record.CreatedAt.After(time.Now().Add(time.Second)) || !record.CreatedAt.Equal(record.UpdatedAt) || record.OAuth != nil {
			t.Fatal("credential metadata or database timestamps differ")
		}
		metadata, err := json.Marshal(record)
		if err != nil || bytes.Contains(metadata, []byte(canary)) {
			t.Fatal("credential metadata contains token plaintext")
		}
		records = append(records, record)
	}
	if records[0].ID == records[1].ID {
		t.Fatal("separate creates reused a credential identity")
	}
	valid := vaults.CreateStaticCredential{TenantID: tenant, VaultID: owned[0].ID, Name: "Rejected", MCPServerURL: "https://mcp.example/tools", Token: opaque}
	if _, err := keyless.CreateStaticCredential(ctx, valid); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("missing encryption key did not fail writes closed")
	}
	for _, target := range []struct{ tenant, vault string }{{tenant, owned[2].ID}, {foreignTenant, owned[0].ID}, {tenant, uuid.NewString()}, {tenant, "invalid"}} {
		command := valid
		command.TenantID, command.VaultID = target.tenant, target.vault
		if _, err := service.CreateStaticCredential(ctx, command); !errors.Is(err, vaults.ErrNotFound) {
			t.Fatal("creation admitted an unowned or missing Vault")
		}
	}
	for _, target := range []struct{ tenant, vault, credential string }{
		{tenant, owned[1].ID, records[0].ID}, {foreignTenant, owned[0].ID, records[0].ID},
		{tenant, owned[2].ID, records[0].ID}, {tenant, uuid.NewString(), records[0].ID}, {tenant, owned[0].ID, uuid.NewString()},
	} {
		if _, err := store.GetCredential(ctx, target.tenant, target.vault, target.credential); !errors.Is(err, vaults.ErrNotFound) {
			t.Fatal("unowned, wrong-Vault or missing credential was disclosed")
		}
	}
	pool.Close()
	store, pool = openStore(t)
	restartedCipher := newCipher(t, bytes.Clone(key))
	wrongKey := bytes.Clone(key)
	wrongKey[0] ^= 1
	wrongCipher := newCipher(t, wrongKey)
	var ciphertexts [][]byte
	for i, record := range records {
		got, err := store.GetCredential(ctx, tenant, record.VaultID, record.ID)
		if err != nil || !reflect.DeepEqual(got, record) {
			t.Fatal("safe metadata recovery changed", err)
		}
		var ciphertext []byte
		var storageType string
		if err := pool.QueryRow(ctx, "SELECT token_ciphertext, pg_typeof(token_ciphertext)::text FROM vault_credentials WHERE id=$1 AND vault_id=$2", record.ID, record.VaultID).Scan(&ciphertext, &storageType); err != nil || storageType != "bytea" || len(ciphertext) < 29 || bytes.Contains(ciphertext, []byte(canary)) {
			t.Fatal("credential ciphertext was not stored as private bytea", err)
		}
		binding := credentialcrypto.Binding{TenantID: tenant, VaultID: record.VaultID, CredentialID: record.ID, AuthType: record.AuthType, Destination: record.MCPServerURL}
		plaintext, err := restartedCipher.Open(ciphertext, binding)
		if err != nil || !bytes.Equal(plaintext, []byte(tokens[i])) {
			t.Fatal("private restart decryption did not preserve token bytes", err)
		}
		if plaintext, err := wrongCipher.Open(ciphertext, binding); err == nil || plaintext != nil {
			t.Fatal("wrong key decrypted persisted ciphertext")
		}
		ciphertexts = append(ciphertexts, ciphertext)
	}
	// Version 1 prefixes the standard library's 12-byte random nonce.
	if bytes.Equal(ciphertexts[0], ciphertexts[1]) || bytes.Equal(ciphertexts[0][1:13], ciphertexts[1][1:13]) {
		t.Fatal("same token in distinct records reused ciphertext or nonce")
	}
	// Change one persisted binding field. Metadata reads still work, while
	// private decryption must reject the altered stored destination.
	if _, err := pool.Exec(ctx, "UPDATE vault_credentials SET mcp_server_url=$1 WHERE id=$2", "https://other.example/tools", records[0].ID); err != nil {
		t.Fatal(err)
	}
	changed, err := store.GetCredential(ctx, tenant, owned[0].ID, records[0].ID)
	if err != nil || changed.MCPServerURL != "https://other.example/tools" {
		t.Fatal("metadata read unexpectedly required decryption", err)
	}
	binding := credentialcrypto.Binding{TenantID: tenant, VaultID: changed.VaultID, CredentialID: changed.ID, AuthType: changed.AuthType, Destination: changed.MCPServerURL}
	if plaintext, err := restartedCipher.Open(ciphertexts[0], binding); err == nil || plaintext != nil {
		t.Fatal("persisted destination substitution authenticated")
	}
	if _, err := pool.Exec(ctx, "UPDATE vault_credentials SET mcp_server_url=$1 WHERE id=$2", records[0].MCPServerURL, records[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, vault := range owned {
		got, err := store.GetVault(ctx, vault.TenantID, vault.ID)
		if err != nil || !reflect.DeepEqual(got, vault) {
			t.Fatal("credential operations changed an owning Vault", err)
		}
	}
	var count, sessions int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM vault_credentials WHERE vault_id=ANY($1::uuid[])), (SELECT count(*) FROM sessions WHERE tenant_id=ANY($2::uuid[]))", []string{owned[0].ID, owned[1].ID, owned[2].ID}, []string{tenant, foreignTenant}).Scan(&count, &sessions); err != nil || count != len(records) || sessions != 0 {
		t.Fatal("rejected requests wrote rows or credential operations created Sessions", err)
	}
}

func TestCredentialListFilteringOwnershipAndKeylessReconnect(t *testing.T) {
	store, pool := openStore(t)
	ctx := t.Context()
	tenant, foreign := uuid.NewString(), uuid.NewString()
	service := newService(t, pool, newCipher(t, make([]byte, 32)), nil)
	var owned []vaults.Vault
	for _, owner := range []string{tenant, tenant, foreign, tenant} {
		owned = append(owned, createVault(t, service, owner))
	}
	// Parent classification does not classify its Credentials.
	if _, err := pool.Exec(ctx, "UPDATE vaults SET status='archived' WHERE id=$1", owned[0].ID); err != nil {
		t.Fatal(err)
	}
	create := func(owner, vault string) vaults.Credential {
		t.Helper()
		return createStatic(t, service, owner, vault, "List fixture", "https://example.invalid/mcp", "synthetic-token-not-public")
	}
	var all []vaults.Credential
	archived := map[string]bool{}
	for i := range 105 {
		c := create(tenant, owned[0].ID)
		status := vaults.StatusActive
		if i%3 == 0 {
			status, archived[c.ID] = vaults.StatusArchived, true
		}
		if _, err := pool.Exec(ctx, "UPDATE vault_credentials SET status=$1, created_at=$2 WHERE id=$3", status, time.Unix(1700000000+int64(i%2), 0), c.ID); err != nil {
			t.Fatal(err)
		}
		c, err := store.GetCredential(ctx, tenant, owned[0].ID, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, c)
	}
	slices.SortFunc(all, func(a, b vaults.Credential) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	otherVault, otherProject := create(tenant, owned[1].ID), create(foreign, owned[2].ID)
	snapshot := func() string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, "SELECT jsonb_agg(to_jsonb(c) ORDER BY id)::text FROM vault_credentials c WHERE vault_id=$1", owned[0].ID).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	read := func(reader vaults.Reader, ascending bool, statuses []string, size int) []vaults.Credential {
		t.Helper()
		actual := []vaults.Credential{}
		cursor := ""
		for {
			page, err := reader.ListCredentials(ctx, tenant, owned[0].ID, vaults.PageQuery{After: cursor, Limit: size, Ascending: ascending, Statuses: statuses})
			if err != nil || len(page.Credentials) == 0 || len(page.Credentials) > size {
				t.Fatal("invalid page", err)
			}
			actual = append(actual, page.Credentials...)
			if len(actual) > len(all) {
				t.Fatal("repeated pagination")
			}
			if page.NextCursor == "" {
				break
			}
			if page.NextCursor != page.Credentials[len(page.Credentials)-1].ID {
				t.Fatal("cursor is not last included Credential")
			}
			cursor = page.NextCursor
		}
		return actual
	}
	for _, ascending := range []bool{true, false} {
		for _, statuses := range [][]string{nil, {vaults.StatusActive}, {vaults.StatusArchived}, {vaults.StatusActive, vaults.StatusArchived}} {
			want := []vaults.Credential{}
			for _, c := range all {
				if len(statuses) != 1 || archived[c.ID] == (statuses[0] == vaults.StatusArchived) {
					want = append(want, c)
				}
			}
			if !ascending {
				slices.Reverse(want)
			}
			for _, size := range []int{20, 100} {
				if got := read(store, ascending, statuses, size); !reflect.DeepEqual(got, want) {
					t.Fatalf("metadata/filter/order mismatch: ascending=%t statuses=%v size=%d", ascending, statuses, size)
				}
			}
		}
	}
	// A parent or cursor ID that cannot name a resource of this list is a
	// missing one.
	for _, tc := range []struct{ owner, vault, cursor string }{
		{tenant, owned[0].ID, otherVault.ID}, {tenant, owned[0].ID, otherProject.ID}, {tenant, owned[0].ID, uuid.NewString()}, {tenant, owned[0].ID, "invalid"},
		{tenant, owned[2].ID, ""}, {foreign, owned[0].ID, ""}, {tenant, uuid.NewString(), ""}, {tenant, "invalid", ""},
	} {
		if _, err := store.ListCredentials(ctx, tc.owner, tc.vault, vaults.PageQuery{After: tc.cursor, Limit: 20}); !errors.Is(err, vaults.ErrNotFound) {
			t.Fatal("unowned/unknown parent or cursor accepted", err)
		}
	}
	for _, tc := range []struct{ vault, cursor string }{{owned[0].ID, all[len(all)-1].ID}, {owned[3].ID, ""}} {
		page, err := store.ListCredentials(ctx, tenant, tc.vault, vaults.PageQuery{After: tc.cursor, Limit: 20, Ascending: true})
		if err != nil || page.Credentials == nil || len(page.Credentials) != 0 || page.NextCursor != "" {
			t.Fatal("empty/terminal page", err)
		}
	}
	for _, tc := range []struct {
		owner string
		query vaults.PageQuery
	}{{"invalid", vaults.PageQuery{Limit: 20}}, {tenant, vaults.PageQuery{Limit: 0}}, {tenant, vaults.PageQuery{Limit: 101}}, {tenant, vaults.PageQuery{Limit: 20, Statuses: []string{"deleted"}}}} {
		if _, err := store.ListCredentials(ctx, tc.owner, owned[0].ID, tc.query); !errors.Is(err, vaults.ErrInvalidInput) {
			t.Fatal("invalid internal query accepted", err)
		}
	}
	pool.Close()
	reopened, pool := openStore(t)
	if got := read(reopened, true, nil, 20); !reflect.DeepEqual(got, all) || snapshot() != before {
		t.Fatal("keyless reads/restart changed metadata, classification or ciphertext")
	}
}

func TestStaticCredentialUpdatePreservesBindingsAndReplacesCurrentSecret(t *testing.T) {
	store, pool := openStore(t)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	cipher := newCipher(t, key)
	service := newService(t, pool, cipher, nil)
	var owned []vaults.Vault
	for _, owner := range []string{tenant, tenant, foreign} {
		owned = append(owned, createVault(t, service, owner))
	}
	firstToken, endpoint := uuid.NewString(), "https://mcp.example/tools"
	original := createStatic(t, service, tenant, owned[0].ID, "Retained name", endpoint, firstToken)
	unrelated := createStatic(t, service, tenant, owned[1].ID, "Unrelated", endpoint, firstToken)
	attached := []string{owned[0].ID}
	// An implicit and an explicit selection freeze the same Credential.
	bindings := []vaults.MCPCredentialBinding{
		resolve(t, service, tenant, attached, vaults.MCPCredentialRequest{ServerLabel: "tools", ServerURL: endpoint})[0],
		resolve(t, service, tenant, attached, vaults.MCPCredentialRequest{ServerLabel: "tools", ServerURL: endpoint, CredentialID: &original.ID})[0],
	}
	readCiphertext := func(id string) []byte {
		t.Helper()
		var ciphertext []byte
		if err := pool.QueryRow(t.Context(), "SELECT token_ciphertext FROM vault_credentials WHERE id=$1", id).Scan(&ciphertext); err != nil {
			t.Fatal("private ciphertext observation failed")
		}
		return ciphertext
	}
	update := func(service *vaults.Service, tenant, vault, id, token string) (vaults.Credential, error) {
		return service.UpdateStaticCredential(t.Context(), vaults.UpdateStaticCredential{TenantID: tenant, VaultID: vault, CredentialID: id, Token: token})
	}
	prior, unrelatedCiphertext := readCiphertext(original.ID), readCiphertext(unrelated.ID)
	lastToken := " \t" + uuid.NewString() + " 雪\n"
	for _, token := range []string{"", lastToken, lastToken} {
		updated, err := update(service, tenant, original.VaultID, original.ID, token)
		if err != nil {
			t.Fatal("token replacement failed")
		}
		want := original
		want.UpdatedAt = updated.UpdatedAt
		if !reflect.DeepEqual(updated, want) || updated.UpdatedAt.Before(original.UpdatedAt) {
			t.Fatal("replacement changed immutable metadata")
		}
		current := readCiphertext(original.ID)
		if bytes.Equal(prior, current) || len(token) > 0 && bytes.Contains(current, []byte(token)) {
			t.Fatal("replacement reused ciphertext or stored plaintext")
		}
		for _, binding := range bindings {
			got, err := bearerToken(t.Context(), service, tenant, attached, binding)
			if err != nil || got != token {
				t.Fatal("existing selection did not read the exact committed replacement")
			}
		}
		prior = current
	}
	before, err := store.GetCredential(t.Context(), tenant, original.VaultID, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertUnchanged := func() {
		t.Helper()
		after, err := store.GetCredential(t.Context(), tenant, original.VaultID, original.ID)
		if err != nil || !reflect.DeepEqual(after, before) || !bytes.Equal(readCiphertext(original.ID), prior) {
			t.Fatal("failed replacement changed the existing row")
		}
	}
	for _, scope := range []struct{ tenant, vault, id string }{
		{foreign, original.VaultID, original.ID}, {tenant, owned[1].ID, original.ID},
		{tenant, owned[2].ID, original.ID}, {tenant, original.VaultID, uuid.NewString()}, {tenant, "invalid", original.ID},
	} {
		if _, err := update(service, scope.tenant, scope.vault, scope.id, "rejected"); !errors.Is(err, vaults.ErrNotFound) {
			t.Fatal("unowned or invalid replacement was admitted")
		}
		assertUnchanged()
	}
	for _, unusable := range []*credentialcrypto.Cipher{nil, {}} {
		if _, err := update(newService(t, pool, unusable, nil), tenant, original.VaultID, original.ID, "rejected"); err == nil {
			t.Fatal("missing or unusable cipher admitted replacement")
		}
		assertUnchanged()
	}
	// A real PostgreSQL mutation failure must preserve both ciphertext and time.
	_, updateErr := update(newService(t, readOnlyPool(t, pool), cipher, nil), tenant, original.VaultID, original.ID, "rejected")
	if !isReadOnlyFailure(updateErr) {
		t.Fatal("database write failure was accepted or translated", updateErr)
	}
	assertUnchanged()
	// A stale destination from a prior metadata read cannot authorize the write.
	_, err = keyedStore(pool, cipher).ReplaceStaticToken(t.Context(), vaults.StaticTokenReplacement{CredentialKey: vaults.CredentialKey{TenantID: tenant, VaultID: original.VaultID, CredentialID: original.ID}, MCPServerURL: endpoint + "/other", Token: "rejected"})
	if !errors.Is(err, vaults.ErrNotFound) {
		t.Fatal("mutation failed to recheck immutable destination", err)
	}
	assertUnchanged()
	// Replacing a damaged old payload needs no old-token decryption.
	if _, err := pool.Exec(t.Context(), "UPDATE vault_credentials SET token_ciphertext=set_byte(token_ciphertext, 15, get_byte(token_ciphertext,15) # 1) WHERE id=$1", original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := update(service, tenant, original.VaultID, original.ID, lastToken); err != nil {
		t.Fatal("replacement tried to decrypt the old token")
	}
	pool.Close()
	store, pool = openStore(t)
	service = newService(t, pool, newCipher(t, bytes.Clone(key)), nil)
	for _, binding := range bindings {
		current, err := bearerToken(t.Context(), service, tenant, attached, binding)
		if err != nil || current != lastToken {
			t.Fatal("reopened dispatch lookup lost the replacement")
		}
	}
	// Competing whole-secret replacements may win in either order, never tear.
	left, right := uuid.NewString()+strings.Repeat("L", 513), uuid.NewString()+strings.Repeat("R", 1025)
	start, results := make(chan struct{}), make(chan error, 2)
	for _, token := range []string{left, right} {
		go func() {
			<-start
			_, err := update(service, tenant, original.VaultID, original.ID, token)
			results <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal("concurrent replacement failed")
		}
	}
	current, err := bearerToken(t.Context(), service, tenant, attached, bindings[0])
	if err != nil || current != left && current != right {
		t.Fatal("concurrent replacements produced an incomplete secret")
	}
	if !bytes.Equal(readCiphertext(unrelated.ID), unrelatedCiphertext) {
		t.Fatal("replacement changed an unrelated Credential")
	}
}

func TestCredentialDeletionScopeBindingAndRestart(t *testing.T) {
	store, pool := openStore(t)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	key := bytes.Repeat([]byte{41}, 32)
	service := newService(t, pool, newCipher(t, key), nil)
	keyless := newService(t, pool, nil, nil)
	vault, wrong := createVault(t, service, tenant), createVault(t, service, tenant)
	original := createStatic(t, service, tenant, vault.ID, "original", "https://mcp.example/tools", "original-secret")
	attached := []string{vault.ID}
	selected := resolve(t, service, tenant, attached, vaults.MCPCredentialRequest{ServerLabel: "tools", ServerURL: original.MCPServerURL})
	retained, err := bearerToken(t.Context(), service, tenant, attached, selected[0])
	if err != nil || retained != "original-secret" {
		t.Fatal("pre-delete dispatch lookup failed")
	}
	sibling := createStatic(t, service, tenant, vault.ID, "sibling", "https://mcp.example/tools", "sibling-secret")
	remove := func(service *vaults.Service, tenant, vault, id string) (string, error) {
		return service.DeleteCredential(t.Context(), vaults.DeleteCredential{TenantID: tenant, VaultID: vault, CredentialID: id})
	}
	for _, scope := range []struct{ tenant, vault, id string }{
		{foreign, vault.ID, original.ID}, {tenant, wrong.ID, original.ID},
		{tenant, vault.ID, uuid.NewString()}, {tenant, "invalid", original.ID}, {tenant, vault.ID, "invalid"},
	} {
		if _, err := remove(keyless, scope.tenant, scope.vault, scope.id); !errors.Is(err, vaults.ErrNotFound) {
			t.Fatal("foreign or invalid delete was accepted", err)
		}
	}
	// An actual database write failure must leave the resource and token intact.
	_, deletionErr := remove(newService(t, readOnlyPool(t, pool), nil, nil), tenant, vault.ID, original.ID)
	if !isReadOnlyFailure(deletionErr) {
		t.Fatal("failed mutation was accepted or translated", deletionErr)
	}
	if value, err := store.GetCredential(t.Context(), tenant, vault.ID, original.ID); err != nil || !reflect.DeepEqual(value, original) {
		t.Fatal("rejected deletion changed the resource", err)
	}
	if token, err := bearerToken(t.Context(), service, tenant, attached, selected[0]); err != nil || token != retained {
		t.Fatal("rejected deletion changed the stored token")
	}
	// Delete without a key, even if the stored payload is damaged.
	if _, err := pool.Exec(t.Context(), "UPDATE vault_credentials SET token_ciphertext=decode('00','hex') WHERE id=$1", original.ID); err != nil {
		t.Fatal(err)
	}
	if id, err := remove(keyless, tenant, vault.ID, original.ID); err != nil || id != original.ID {
		t.Fatal("keyless deletion failed", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM vault_credentials WHERE id=$1", original.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted row or ciphertext remains")
	}
	pool.Close()
	store, pool = openStore(t)
	service = newService(t, pool, newCipher(t, bytes.Clone(key)), nil)
	if _, err := remove(service, tenant, vault.ID, original.ID); !errors.Is(err, vaults.ErrNotFound) {
		t.Fatal("repeat deletion did not stay absent")
	}
	if _, err := store.GetCredential(t.Context(), tenant, vault.ID, original.ID); !errors.Is(err, vaults.ErrNotFound) {
		t.Fatal("deleted metadata reappeared after restart")
	}
	if _, err := service.UpdateStaticCredential(t.Context(), vaults.UpdateStaticCredential{TenantID: tenant, VaultID: vault.ID, CredentialID: original.ID, Token: "replacement"}); !errors.Is(err, vaults.ErrNotFound) {
		t.Fatal("replacement resurrected a deleted credential")
	}
	if _, err := bearerToken(t.Context(), service, tenant, attached, selected[0]); !errors.Is(err, vaults.ErrNotFound) {
		t.Fatal("frozen selection fell back to another token")
	}
	_, err = service.ResolveMCPCredentials(t.Context(), vaults.ResolveMCPCredentials{TenantID: tenant, VaultIDs: attached, Requests: []vaults.MCPCredentialRequest{{ServerLabel: "tools", ServerURL: original.MCPServerURL, CredentialID: &original.ID}}})
	if !isSelectionError(err, false, "MCP credential_id "+original.ID+" was not found in an attached vault") {
		t.Fatal("deleted explicit selection was admitted", err)
	}
	page, err := store.ListCredentials(t.Context(), tenant, vault.ID, vaults.PageQuery{Limit: 100, Ascending: true})
	if err != nil || len(page.Credentials) != 1 || !reflect.DeepEqual(page.Credentials[0], sibling) {
		t.Fatal("deletion changed a sibling or list membership", err)
	}
	if value, err := store.GetVault(t.Context(), tenant, vault.ID); err != nil || !reflect.DeepEqual(value, vault) {
		t.Fatal("deletion changed its parent Vault")
	}
}

func TestCredentialDeletionConcurrentReplacementCannotResurrect(t *testing.T) {
	store, pool := openStore(t)
	tenant := uuid.NewString()
	service := newService(t, pool, newCipher(t, bytes.Repeat([]byte{42}, 32)), nil)
	vault := createVault(t, service, tenant)
	for range 8 {
		value := createStatic(t, service, tenant, vault.ID, "competing", "https://mcp.example/tools", "before")
		start, updated := make(chan struct{}), make(chan error, 1)
		go func() {
			<-start
			_, err := service.UpdateStaticCredential(t.Context(), vaults.UpdateStaticCredential{TenantID: tenant, VaultID: vault.ID, CredentialID: value.ID, Token: "after"})
			updated <- err
		}()
		close(start)
		_, deleted := service.DeleteCredential(t.Context(), vaults.DeleteCredential{TenantID: tenant, VaultID: vault.ID, CredentialID: value.ID})
		updateErr := <-updated
		if deleted != nil || updateErr != nil && !errors.Is(updateErr, vaults.ErrNotFound) {
			t.Fatal("competing update/delete failed unexpectedly", deleted, updateErr)
		}
		if _, err := store.GetCredential(t.Context(), tenant, vault.ID, value.ID); !errors.Is(err, vaults.ErrNotFound) {
			t.Fatal("concurrent update resurrected deleted metadata")
		}
	}
}
