package sessionpg

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestListAdminRuntimeTargetsPagesLiveSessionsOfTenants(t *testing.T) {
	pool := pgtest.Open(t)
	store := New(pgunit.NewPool(pool), nil)
	first, second, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	created := time.Now().UTC().Add(-time.Hour)
	session := func(tenant string, offset time.Duration, deleted bool) sessions.AdminRuntimeTarget {
		id := uuid.NewString()
		exec(t, pool, `INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash, created_at, deleted_at)
			VALUES ($1, $2, 'codex', $5, 'hash', $3, CASE WHEN $4::boolean THEN clock_timestamp() END)`, id, tenant, created.Add(offset), deleted, id)
		return sessions.AdminRuntimeTarget{SessionID: id, TenantID: tenant}
	}
	a := session(first, 0, false)
	b := session(second, time.Second, false)
	c := session(first, 2*time.Second, false)
	deleted := session(first, 3*time.Second, true)
	other := session(foreign, 4*time.Second, false)
	tenants := []string{first, second}

	page, err := store.ListAdminRuntimeTargets(t.Context(), tenants, "", 2, true)
	if err != nil || !page.HasMore || !reflect.DeepEqual(page.Data, []sessions.AdminRuntimeTarget{a, b}) {
		t.Fatalf("first ascending page = %+v, %v", page, err)
	}
	page, err = store.ListAdminRuntimeTargets(t.Context(), tenants, b.SessionID, 2, true)
	if err != nil || page.HasMore || !reflect.DeepEqual(page.Data, []sessions.AdminRuntimeTarget{c}) {
		t.Fatalf("second ascending page = %+v, %v", page, err)
	}
	page, err = store.ListAdminRuntimeTargets(t.Context(), tenants, "", 100, false)
	if err != nil || page.HasMore || !reflect.DeepEqual(page.Data, []sessions.AdminRuntimeTarget{c, b, a}) {
		t.Fatalf("descending page = %+v, %v", page, err)
	}
	page, err = store.ListAdminRuntimeTargets(t.Context(), []string{}, "", 20, false)
	if err != nil || page.HasMore || page.Data == nil || len(page.Data) != 0 {
		t.Fatalf("page without tenants = %+v, %v", page, err)
	}
	for _, after := range []string{deleted.SessionID, other.SessionID, "not-a-session"} {
		if _, err := store.ListAdminRuntimeTargets(t.Context(), tenants, after, 20, false); !errors.Is(err, sessions.ErrNotFound) {
			t.Fatalf("cursor %q: %v", after, err)
		}
	}
	for _, limit := range []int{0, 101} {
		if _, err := store.ListAdminRuntimeTargets(t.Context(), tenants, "", limit, false); !errors.Is(err, sessions.ErrInvalidInput) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	if _, err := store.ListAdminRuntimeTargets(t.Context(), []string{"not-a-tenant"}, "", 20, false); !errors.Is(err, sessions.ErrInvalidInput) {
		t.Fatalf("malformed tenant: %v", err)
	}
}
