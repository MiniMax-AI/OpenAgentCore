package execution

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// heldLease is an execution lease that stays held, for lifecycle tests that run
// no execution query: its cancellation fence cancels immediately.
type heldLease struct{}

func (heldLease) CheckOwnership(context.Context) error { return nil }
func (heldLease) CancelOperations(_ context.Context, cancel context.CancelFunc) error {
	cancel()
	return nil
}
func (heldLease) Close(context.Context) error { return nil }

func testRuntimeManager(t *testing.T) *runtimeManager {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	m := &runtimeManager{lease: heldLease{}, config: RuntimeProvider{ProviderKind: "docker"}, ctx: ctx, cancel: cancel, nodes: make(map[string]*runtimeNode), failed: make(chan error, 1), inventory: make(chan struct{}, 1)}
	t.Cleanup(func() { m.stop(); m.drain() })
	return m
}

func waitManager(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("manager condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRuntimeManagerOldInventoryPreservesNewDirectLane(t *testing.T) {
	m := testRuntimeManager(t)
	a, err := m.node("a")
	if err != nil {
		t.Fatal(err)
	}
	entered, finish, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		previous := m.snapshotNodes()
		close(entered)
		<-finish // inventory read started before node b was registered.
		_, err := m.applyInventory(previous, []string{"a"})
		done <- err
	}()
	<-entered
	b, err := m.node("b")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.lifecycle.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := b.lifecycle.lock(t.Context()); err != nil {
		t.Fatal("another node's gate blocked direct provisioning", err)
	}
	<-a.lifecycle.gate
	<-b.lifecycle.gate
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	retained, err := m.node("b")
	if err != nil || retained != b || b.lifecycle.ctx.Err() != nil {
		t.Fatal("old inventory retired newly registered direct lane")
	}
}

func TestRuntimeManagerRetirementDrainsBeforeReplacingGate(t *testing.T) {
	m := testRuntimeManager(t)
	a, _ := m.node("a")
	b, _ := m.node("b")
	if err := a.lifecycle.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err := m.applyInventory(m.snapshotNodes(), []string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	if a.lifecycle.ctx.Err() == nil {
		t.Fatal("removed node was not canceled")
	}
	if _, err := m.node("a"); !errors.Is(err, ErrExecutionUnavailable) {
		t.Fatal("retiring node acquired a new gate", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := b.lifecycle.lock(ctx); err != nil {
		t.Fatal("retirement blocked another node", err)
	}
	<-b.lifecycle.gate
	<-a.lifecycle.gate
	waitManager(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.nodes["a"] == nil })
	next, err := m.node("a")
	if err != nil || next == a {
		t.Fatal("retired gate not released", err)
	}
}

func TestRuntimeManagerStopRejectsEntrantsAndDrainsDirectCall(t *testing.T) {
	m := testRuntimeManager(t)
	ctx, finish, err := m.enter(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := m.node("a")
	if err := a.lifecycle.lock(ctx); err != nil {
		t.Fatal(err)
	}
	var closers sync.WaitGroup
	for range 8 {
		closers.Add(1)
		go func() { defer closers.Done(); m.stop() }()
	}
	closers.Wait()
	if _, _, err := m.enter(t.Context()); !errors.Is(err, ErrExecutionUnavailable) {
		t.Fatal("new caller entered stopped manager")
	}
	if _, err := m.node("new"); !errors.Is(err, ErrExecutionUnavailable) {
		t.Fatal("new node entered stopped manager")
	}
	if err := a.lifecycle.lock(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatal("queued caller not canceled", err)
	}
	drained := make(chan struct{})
	go func() { m.drain(); close(drained) }()
	select {
	case <-drained:
		t.Fatal("lease could be released before direct operation returned")
	default:
	}
	<-a.lifecycle.gate
	finish()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("manager failed to drain")
	}
}

func TestRuntimeManagerHintsRemainPerNode(t *testing.T) {
	m := testRuntimeManager(t)
	a, _ := m.node("a")
	b, _ := m.node("b")
	for range 100 {
		select {
		case m.hints("a") <- struct{}{}:
		default:
		}
	}
	if len(a.lifecycle.wakeHints) != 1 || len(b.lifecycle.wakeHints) != 0 {
		t.Fatal("hint burst crossed nodes or grew queue")
	}
	m.stop()
	select {
	case m.hints("a") <- struct{}{}:
		t.Fatal("stopped manager accepted hint")
	default:
	}
}
