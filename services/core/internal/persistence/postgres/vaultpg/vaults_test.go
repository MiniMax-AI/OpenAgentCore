package vaultpg_test

import (
	"bytes"
	"cmp"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/vaultpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

func TestVaultsPersistAndStayTenantScoped(t *testing.T) {
	store, pool := openStore(t)
	service := newService(t, store, nil, nil)
	ctx := t.Context()
	tenantA, tenantB := uuid.NewString(), uuid.NewString()
	before := time.Now().Add(-time.Second)
	unnamed, err := service.CreateVault(ctx, vaults.CreateVault{TenantID: tenantA})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(unnamed.ID); err != nil || unnamed.TenantID != tenantA || unnamed.Name != nil || unnamed.Metadata == nil || len(unnamed.Metadata) != 0 || unnamed.CreatedAt.Before(before) || unnamed.CreatedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("unexpected unnamed vault: %+v, %v", unnamed, err)
	}
	// Validate the byte boundary with multibyte text, without Session metadata
	// count or character limits. Public name trimming belongs to the API layer.
	name := strings.Repeat("é", 128)
	command := vaults.CreateVault{TenantID: tenantA, Name: &name, Metadata: map[string]string{"": "", "purpose": "保存 configuration"}}
	named, err := service.CreateVault(ctx, command)
	if err != nil || named.ID == unnamed.ID || named.Name == nil || *named.Name != name || !reflect.DeepEqual(named.Metadata, command.Metadata) {
		t.Fatalf("unexpected named vault: %+v, %v", named, err)
	}
	for _, tenant := range []string{tenantA, tenantB} {
		command.TenantID = tenant
		other, err := service.CreateVault(ctx, command)
		if err != nil || other.ID == named.ID || other.TenantID != tenant {
			t.Fatalf("distinct resource creation: %+v, %v", other, err)
		}
	}
	for _, lookup := range []struct{ tenant, id string }{{tenantB, named.ID}, {tenantA, uuid.NewString()}} {
		if _, err := store.GetVault(ctx, lookup.tenant, lookup.id); !errors.Is(err, vaults.ErrNotFound) {
			t.Fatalf("unowned/unknown vault lookup: %v", err)
		}
	}
	// Recreate the pool and Store as a restarted standalone service would.
	pool.Close()
	reopened, pool := openStore(t)
	for _, want := range []vaults.Vault{unnamed, named} {
		got, err := reopened.GetVault(ctx, tenantA, want.ID)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("durable read: %+v, %v; want %+v", got, err, want)
		}
	}
	var sessions int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE tenant_id = $1", tenantA).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("Vault creation produced %d Sessions: %v", sessions, err)
	}
}

func TestVaultsRejectInvalidInputWithoutWrites(t *testing.T) {
	store, pool := openStore(t)
	service := newService(t, store, nil, nil)
	ctx := t.Context()
	tenant := uuid.NewString()
	for _, name := range []string{"", strings.Repeat("x", 257), strings.Repeat("é", 129), string([]byte{0xff})} {
		if _, err := service.CreateVault(ctx, vaults.CreateVault{TenantID: tenant, Name: &name}); !errors.Is(err, vaults.ErrInvalidInput) {
			t.Fatalf("invalid name length %d: %v", len(name), err)
		}
	}
	for _, invalid := range []string{"", "not-a-uuid", uuid.Nil.String()} {
		if _, err := service.CreateVault(ctx, vaults.CreateVault{TenantID: invalid}); !errors.Is(err, vaults.ErrInvalidInput) {
			t.Fatalf("invalid create tenant accepted: %v", err)
		}
		if _, err := store.GetVault(ctx, invalid, uuid.NewString()); !errors.Is(err, vaults.ErrInvalidInput) {
			t.Fatalf("invalid read tenant accepted: %v", err)
		}
		if _, err := store.GetVault(ctx, tenant, invalid); !errors.Is(err, vaults.ErrInvalidInput) {
			t.Fatalf("invalid vault ID accepted: %v", err)
		}
	}
	if _, err := service.CreateVault(ctx, vaults.CreateVault{TenantID: tenant, Metadata: map[string]string{"large": strings.Repeat("x", 64*1024)}}); !errors.Is(err, vaults.ErrInvalidInput) {
		t.Fatalf("oversized metadata accepted: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM vaults WHERE tenant_id = $1", tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid input created %d Vaults: %v", count, err)
	}
}

func TestVaultListFilteringPaginationAndReconnect(t *testing.T) {
	store, pool := openStore(t)
	service := newService(t, store, nil, nil)
	ctx := t.Context()
	tenant, other := uuid.NewString(), uuid.NewString()
	empty, err := store.ListVaults(ctx, tenant, vaults.PageQuery{Limit: 20})
	if err != nil || empty.Vaults == nil || len(empty.Vaults) != 0 || empty.NextCursor != "" {
		t.Fatalf("empty page: %+v, %v", empty, err)
	}
	var all []vaults.Vault
	archived := map[string]bool{}
	for i := range 105 {
		vault, err := service.CreateVault(ctx, vaults.CreateVault{TenantID: tenant, Metadata: map[string]string{"purpose": "safe list fixture"}})
		if err != nil {
			t.Fatal(err)
		}
		status := vaults.StatusActive
		if i%3 == 0 {
			status, archived[vault.ID] = vaults.StatusArchived, true
		}
		// Synthetic classifications exercise reads, not a public archive lifecycle.
		if _, err := pool.Exec(ctx, "UPDATE vaults SET created_at=$1, status=$2 WHERE tenant_id=$3 AND id=$4", time.Unix(1700000000+int64(i%2), 0).UTC(), status, tenant, vault.ID); err != nil {
			t.Fatal(err)
		}
		vault, err = store.GetVault(ctx, tenant, vault.ID)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, vault)
	}
	slices.SortFunc(all, func(a, b vaults.Vault) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	foreign := createVault(t, service, other)
	read := func(reader vaults.Reader, ascending bool, statuses []string, size int) []vaults.Vault {
		t.Helper()
		actual := []vaults.Vault{}
		cursor := ""
		for {
			page, err := reader.ListVaults(ctx, tenant, vaults.PageQuery{After: cursor, Limit: size, Ascending: ascending, Statuses: statuses})
			if err != nil || len(page.Vaults) == 0 || len(page.Vaults) > size {
				t.Fatalf("page: %+v, %v", page, err)
			}
			actual = append(actual, page.Vaults...)
			if len(actual) > len(all) {
				t.Fatal("pagination repeated records")
			}
			if page.NextCursor == "" {
				break
			}
			if page.NextCursor != page.Vaults[len(page.Vaults)-1].ID {
				t.Fatal("cursor is not the last included resource")
			}
			cursor = page.NextCursor
		}
		return actual
	}
	for _, ascending := range []bool{true, false} {
		for _, statuses := range [][]string{nil, {vaults.StatusActive}, {vaults.StatusArchived}, {vaults.StatusActive, vaults.StatusArchived}} {
			want := []vaults.Vault{}
			for _, vault := range all {
				if len(statuses) != 1 || archived[vault.ID] == (statuses[0] == vaults.StatusArchived) {
					want = append(want, vault)
				}
			}
			if !ascending {
				slices.Reverse(want)
			}
			for _, size := range []int{20, 100} {
				if got := read(store, ascending, statuses, size); !reflect.DeepEqual(got, want) {
					t.Fatalf("filtered ordering/projection mismatch: ascending=%t statuses=%v size=%d got=%d want=%d", ascending, statuses, size, len(got), len(want))
				}
			}
		}
	}
	for _, cursor := range []string{foreign.ID, uuid.NewString(), "invalid"} {
		if _, err := store.ListVaults(ctx, tenant, vaults.PageQuery{After: cursor, Limit: 20, Ascending: true, Statuses: []string{vaults.StatusArchived}}); !errors.Is(err, vaults.ErrNotFound) {
			t.Fatalf("foreign/unknown/malformed cursor: %v", err)
		}
	}
	for _, tc := range []struct {
		tenant string
		query  vaults.PageQuery
	}{{"invalid", vaults.PageQuery{Limit: 20}}, {tenant, vaults.PageQuery{Limit: 0}}, {tenant, vaults.PageQuery{Limit: 101}}, {tenant, vaults.PageQuery{Limit: 20, Statuses: []string{"deleted"}}}} {
		if _, err := store.ListVaults(ctx, tc.tenant, tc.query); !errors.Is(err, vaults.ErrInvalidInput) {
			t.Fatalf("invalid store query: %v", err)
		}
	}
	tail, err := store.ListVaults(ctx, tenant, vaults.PageQuery{After: all[len(all)-1].ID, Limit: 100, Ascending: true})
	if err != nil || tail.Vaults == nil || len(tail.Vaults) != 0 || tail.NextCursor != "" {
		t.Fatalf("terminal page: %+v, %v", tail, err)
	}
	page, err := store.ListVaults(ctx, other, vaults.PageQuery{Limit: 100})
	if err != nil || !reflect.DeepEqual(page.Vaults, []vaults.Vault{foreign}) || page.NextCursor != "" {
		t.Fatalf("project isolation: %+v, %v", page, err)
	}
	pool.Close()
	reopened, _ := openStore(t)
	if got := read(reopened, true, nil, 20); !reflect.DeepEqual(got, all) {
		t.Fatal("listing changed after reconnect")
	}
}

func TestVaultDeletionCascadeBindingAndRestart(t *testing.T) {
	store, pool := openStore(t)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	key := bytes.Repeat([]byte{43}, 32)
	service := newService(t, store, newCipher(t, key), nil)
	keyless := newService(t, store, nil, nil)
	vault, retained, empty := createVault(t, service, tenant), createVault(t, service, tenant), createVault(t, service, tenant)
	original := createStatic(t, service, tenant, vault.ID, "original", "https://mcp.example/tools", "original-secret")
	attached := []string{vault.ID, retained.ID}
	selected := resolve(t, service, tenant, attached, vaults.MCPCredentialRequest{ServerLabel: "tools", ServerURL: original.MCPServerURL})
	extra := createStatic(t, service, tenant, vault.ID, "archived", "https://mcp.example/other", "archived-secret")
	sibling := createStatic(t, service, tenant, retained.ID, "sibling", original.MCPServerURL, "sibling-secret")
	if _, err := pool.Exec(t.Context(), "UPDATE vaults SET status='archived' WHERE id=$1", vault.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE vault_credentials SET status='archived' WHERE id=$1", extra.ID); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []struct{ tenant, id string }{{foreign, vault.ID}, {tenant, uuid.NewString()}, {tenant, "invalid"}, {"invalid", vault.ID}} {
		if _, err := keyless.DeleteVault(t.Context(), vaults.DeleteVault{TenantID: scope.tenant, VaultID: scope.id}); !errors.Is(err, vaults.ErrNotFound) {
			t.Fatal("foreign or invalid deletion was accepted", err)
		}
	}
	_, deletionErr := newService(t, readOnlyStore(t, pool), nil, nil).DeleteVault(t.Context(), vaults.DeleteVault{TenantID: tenant, VaultID: vault.ID})
	if !isReadOnlyFailure(deletionErr) {
		t.Fatal("failed mutation was accepted or translated", deletionErr)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	// Verify the database cascade independently inside an explicit transaction.
	if _, err := sqlc.New(tx).DeleteVault(t.Context(), sqlc.DeleteVaultParams{TenantID: pgunit.PathID(tenant), ID: pgunit.PathID(vault.ID)}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM vaults WHERE id=$1)+(SELECT count(*) FROM vault_credentials WHERE vault_id=$1)", vault.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("cascade was not visible in the deletion transaction", err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if value, err := store.GetVault(t.Context(), tenant, vault.ID); err != nil || !reflect.DeepEqual(value, vault) {
		t.Fatal("rollback changed the Vault", err)
	}
	for _, expected := range []vaults.Credential{original, extra} {
		if value, err := store.GetCredential(t.Context(), tenant, vault.ID, expected.ID); err != nil || !reflect.DeepEqual(value, expected) {
			t.Fatal("rollback changed a child", err)
		}
	}
	if token, err := bearerToken(t.Context(), service, tenant, attached, selected[0]); err != nil || token != "original-secret" {
		t.Fatal("rejected deletion changed the stored token")
	}
	if _, err := pool.Exec(t.Context(), "UPDATE vault_credentials SET token_ciphertext=decode('00','hex') WHERE vault_id=$1", vault.ID); err != nil {
		t.Fatal(err)
	}
	for _, target := range []vaults.Vault{empty, vault} {
		if id, err := keyless.DeleteVault(t.Context(), vaults.DeleteVault{TenantID: tenant, VaultID: target.ID}); err != nil || id != target.ID {
			t.Fatal("keyless deletion failed", err)
		}
	}
	if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM vaults WHERE id=$1)+(SELECT count(*) FROM vault_credentials WHERE vault_id=$1)", vault.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("committed parent or encrypted children remain", err)
	}
	pool.Close()
	store, _ = openStore(t)
	service = newService(t, store, newCipher(t, bytes.Clone(key)), nil)
	for _, target := range []vaults.Vault{empty, vault} {
		if _, err := store.GetVault(t.Context(), tenant, target.ID); !errors.Is(err, vaults.ErrNotFound) {
			t.Fatal("deleted Vault reappeared after restart")
		}
		if _, err := service.DeleteVault(t.Context(), vaults.DeleteVault{TenantID: tenant, VaultID: target.ID}); !errors.Is(err, vaults.ErrNotFound) {
			t.Fatal("repeated deletion did not remain absent")
		}
	}
	for _, child := range []vaults.Credential{original, extra} {
		if _, err := store.GetCredential(t.Context(), tenant, vault.ID, child.ID); !errors.Is(err, vaults.ErrNotFound) {
			t.Fatal("deleted child reappeared")
		}
		if _, err := service.UpdateStaticCredential(t.Context(), vaults.UpdateStaticCredential{TenantID: tenant, VaultID: vault.ID, CredentialID: child.ID, Token: "replacement"}); !errors.Is(err, vaults.ErrNotFound) {
			t.Fatal("replacement recreated a deleted child")
		}
	}
	if _, err := service.CreateStaticCredential(t.Context(), vaults.CreateStaticCredential{TenantID: tenant, VaultID: vault.ID, Name: "late", MCPServerURL: original.MCPServerURL, Token: "late"}); !errors.Is(err, vaults.ErrNotFound) {
		t.Fatal("new child was admitted under a deleted Vault")
	}
	if _, err := bearerToken(t.Context(), service, tenant, attached, selected[0]); !errors.Is(err, vaults.ErrNotFound) {
		t.Fatal("frozen binding reselected a credential in another attached Vault")
	}
	if _, err := store.ListCredentials(t.Context(), tenant, vault.ID, vaults.PageQuery{Limit: 100, Ascending: true}); !errors.Is(err, vaults.ErrNotFound) {
		t.Fatal("deleted parent remained listable")
	}
	if value, err := store.GetVault(t.Context(), tenant, retained.ID); err != nil || !reflect.DeepEqual(value, retained) {
		t.Fatal("deletion changed another Vault")
	}
	if value, err := store.GetCredential(t.Context(), tenant, retained.ID, sibling.ID); err != nil || !reflect.DeepEqual(value, sibling) {
		t.Fatal("deletion changed another Vault's credential")
	}
}

func TestVaultDeletionConcurrentChildMutations(t *testing.T) {
	store, pool := openStore(t)
	tenant := uuid.NewString()
	service := newService(t, store, newCipher(t, bytes.Repeat([]byte{44}, 32)), nil)
	// Each operation runs on its own Store, so only PostgreSQL orders them.
	other := func() *vaults.Service {
		return newService(t, vaultpg.New(pgunit.NewPool(pool)), newCipher(t, bytes.Repeat([]byte{44}, 32)), nil)
	}
	for range 8 {
		vault := createVault(t, service, tenant)
		value := createStatic(t, service, tenant, vault.ID, "competing", "https://mcp.example/tools", "before")
		start, created, updated, removed := make(chan struct{}), make(chan error, 1), make(chan error, 1), make(chan error, 1)
		creator, updater, remover := other(), other(), other()
		go func() {
			<-start
			_, err := creator.CreateStaticCredential(t.Context(), vaults.CreateStaticCredential{TenantID: tenant, VaultID: vault.ID, Name: "competing", MCPServerURL: "https://mcp.example/tools", Token: "before"})
			created <- err
		}()
		go func() {
			<-start
			_, err := updater.UpdateStaticCredential(t.Context(), vaults.UpdateStaticCredential{TenantID: tenant, VaultID: vault.ID, CredentialID: value.ID, Token: "after"})
			updated <- err
		}()
		go func() {
			<-start
			_, err := remover.DeleteCredential(t.Context(), vaults.DeleteCredential{TenantID: tenant, VaultID: vault.ID, CredentialID: value.ID})
			removed <- err
		}()
		close(start)
		_, deleted := service.DeleteVault(t.Context(), vaults.DeleteVault{TenantID: tenant, VaultID: vault.ID})
		createErr, updateErr, removeErr := <-created, <-updated, <-removed
		for _, err := range []error{createErr, updateErr, removeErr} {
			if err != nil && !errors.Is(err, vaults.ErrNotFound) {
				t.Fatal("competing mutation failed unexpectedly", err)
			}
		}
		if deleted != nil {
			t.Fatal("Vault deletion failed", deleted)
		}
		var count int
		if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM vaults WHERE id=$1)+(SELECT count(*) FROM vault_credentials WHERE vault_id=$1)", vault.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("concurrent mutation resurrected deleted resources", err)
		}
	}
}
