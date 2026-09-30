package node

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func budgetRequest() request {
	return request{ID: uuid.NewString(), Operation: "info", Reference: reference(), TimeoutMillis: 1500}
}

func TestRequestBudgetUsesReceivingClock(t *testing.T) {
	sent := budgetRequest()
	// A Core-local timestamp must never be serialized, even when accidentally set.
	sent.deadline = time.Now().Add(-6 * time.Minute)
	data, err := json.Marshal(sent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "deadline") || !strings.Contains(string(data), "timeout_ms") {
		t.Fatalf("wire contains wall clock deadline: %s", data)
	}
	for _, skew := range []time.Duration{-6 * time.Minute, 6 * time.Minute} {
		var received request
		if err = json.Unmarshal(data, &received); err != nil {
			t.Fatal(err)
		}
		nodeNow := time.Now().Add(skew)
		if err = received.receive(nodeNow); err != nil {
			t.Fatal(err)
		}
		if got := received.deadline.Sub(nodeNow); got != 1500*time.Millisecond {
			t.Fatalf("clock skew %s changed budget: %s", skew, got)
		}
	}
}

func TestRequestBudgetBounds(t *testing.T) {
	for _, millis := range []int64{-1, 0, maxRequestTimeoutMillis + 1, 1 << 62} {
		q := budgetRequest()
		q.TimeoutMillis = millis
		if err := q.receive(time.Now()); !errors.Is(err, sandbox.ErrInvalid) {
			t.Fatalf("accepted timeout %d: %v", millis, err)
		}
	}
	for _, millis := range []int64{1, maxRequestTimeoutMillis} {
		q := budgetRequest()
		q.TimeoutMillis = millis
		if err := q.receive(time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	q := budgetRequest()
	if err := q.setTimeout(time.Now().Add(5 * time.Minute)); err != nil || q.TimeoutMillis != maxRequestTimeoutMillis {
		t.Fatal("sender failed to cap timeout", err)
	}
	if err := q.setTimeout(time.Now().Add(-time.Millisecond)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("sender accepted expired deadline", err)
	}
	if err := q.setTimeout(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if q.TimeoutMillis < 1900 || q.TimeoutMillis > 2000 {
		t.Fatalf("wrong remaining budget: %d", q.TimeoutMillis)
	}
}

func TestQueuedMutationExpiresWithoutExecution(t *testing.T) {
	id := identity()
	hub := NewHub(HubOptions{Authenticate: func(context.Context, string, string) (Identity, error) { return id, nil }, OwnerEpoch: func(context.Context) (uint64, error) { return 1, nil }})
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	dir := stateDir(t)
	stored, err := InitIdentity(dir, server.URL, id)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{started: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, AgentConfig{CoreURL: server.URL, StateDirectory: dir, Identity: id, Credential: stored.Credential, Provider: p, Probe: probe})
	}()
	released := false
	defer func() {
		if !released {
			close(p.release)
		}
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	wait(t, func() bool { return hub.Online(id.NodeID) })
	proxy := hub.Proxy(id.NodeID, "docker", 1)
	r := reference()
	createCtx, stopCreate := context.WithTimeout(ctx, 3*time.Second)
	defer stopCreate()
	created := make(chan error, 1)
	go func() { _, err := proxy.Create(createCtx, sandbox.Bootstrap{Reference: r}); created <- err }()
	<-p.started
	killCtx, stopKill := context.WithTimeout(ctx, 80*time.Millisecond)
	defer stopKill()
	if err = proxy.Kill(killCtx, r); !errors.Is(err, sandbox.ErrComputeUnconfirmed) {
		t.Fatalf("queued mutation outcome = %v", err)
	}
	// The node has received the frame but its sole worker is still in Create.
	// Its locally anchored deadline continues to run while waiting in that queue.
	time.Sleep(120 * time.Millisecond)
	close(p.release)
	released = true
	if err = <-created; err != nil {
		t.Fatal(err)
	}
	// This subsequent read is serialized behind the expired mutation.
	if _, err = proxy.GetInfo(ctx, r); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.kills != 0 || p.creates != 1 {
		t.Fatalf("expired mutation executed/replayed: creates=%d kills=%d", p.creates, p.kills)
	}
}
