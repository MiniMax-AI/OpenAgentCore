package integration

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/automations"
	"github.com/google/uuid"
)

// Concurrent recording makes one invocation per occurrence, and settling is
// idempotent: the first outcome stays and completes the one-time Automation.
func TestAutomationRecordingAndSettlement(t *testing.T) {
	s, _ := testStore(t)
	store, _ := fixtureAutomations(t, s)
	tenant, project := uuid.NewString(), uuid.NewString()
	if _, err := s.pool.Exec(t.Context(), "INSERT INTO execution_project_scopes(tenant_id,organization_id,project_id) VALUES($1,'test-org',$2)", tenant, project); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "INSERT INTO projects(id,name,tenant_id,subject_kind,subject_id) VALUES($1,'Automation store',$1,'service_account','test-runner')", tenant); err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(-time.Minute)
	created, err := store.Create(t.Context(), automations.Automation{TenantID: tenant, Type: automations.Once, Name: "due", Metadata: map[string]string{}, Status: automations.Active, RunAt: &due, NextRunAt: &due,
		TargetType: "existing_session", Target: []byte(`{"type":"existing_session"}`), Contents: []byte(`{"session_id":"s","instructions":"i"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var recorded atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := store.Record(t.Context(), time.Now())
			if err != nil {
				t.Error(err)
			}
			recorded.Add(int64(n))
		}()
	}
	wg.Wait()
	page, err := store.ListInvocations(t.Context(), tenant, created.ID, automations.ListQuery{Limit: 10})
	if err != nil || recorded.Load() != 1 || len(page.Invocations) != 1 || page.Invocations[0].Status != "queued" {
		t.Fatalf("recorded %d: %+v %v", recorded.Load(), page, err)
	}
	deliveries, err := store.Deliverable(t.Context(), 10)
	if err != nil || len(deliveries) != 1 || string(deliveries[0].Request) != `{"session_id":"s","instructions":"i"}` || deliveries[0].Scope.ProjectID != project {
		t.Fatalf("deliverable %+v %v", deliveries, err)
	}
	id := deliveries[0].InvocationID
	if err := store.Settle(t.Context(), automations.Outcome{InvocationID: id, Status: "completed", SessionID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	if err := store.Settle(t.Context(), automations.Outcome{InvocationID: id, Status: "failed", Code: "late", Message: "late"}); err != nil {
		t.Fatal(err)
	}
	invocation, err := store.GetInvocation(t.Context(), tenant, created.ID, id)
	if err != nil || invocation.Status != "completed" || invocation.Code != "" {
		t.Fatalf("settled twice: %+v %v", invocation, err)
	}
	if a, err := store.Get(t.Context(), tenant, created.ID); err != nil || a.Status != automations.Completed {
		t.Fatalf("one-time automation after its invocation settled: %+v %v", a, err)
	}
	if left, err := store.Deliverable(t.Context(), 10); err != nil || len(left) != 0 {
		t.Fatalf("settled invocation stays deliverable: %+v %v", left, err)
	}

	// An occurrence that finds a queued invocation is recorded as skipped, with the target Session.
	session := uuid.NewString()
	periodic, err := store.Create(t.Context(), automations.Automation{TenantID: tenant, Type: automations.Periodic, Name: "busy", Metadata: map[string]string{}, Status: automations.Active, Expression: "* * * * *", Timezone: "UTC", NextRunAt: &due,
		TargetType: "existing_session", Target: []byte(`{"type":"existing_session","session_id":"` + session + `"}`), Contents: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Invoke(t.Context(), tenant, periodic.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := store.Record(t.Context(), time.Now()); err != nil || n != 1 {
		t.Fatalf("recorded %d: %v", n, err)
	}
	page, err = store.ListInvocations(t.Context(), tenant, periodic.ID, automations.ListQuery{Limit: 10, Ascending: true})
	if err != nil || len(page.Invocations) != 2 || page.Invocations[0].Status != "queued" || page.Invocations[1].Status != "skipped" || page.Invocations[1].SkipReason != "pending_delivery" || page.Invocations[1].SessionID != session {
		t.Fatalf("skipped occurrence: %+v %v", page, err)
	}
}
