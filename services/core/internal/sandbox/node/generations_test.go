package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	"github.com/google/uuid"
)

func generationFixture(count int) *GenerationManager {
	m := &GenerationManager{values: map[uint64]*localGeneration{}, wake: make(chan struct{}, 1)}
	for i := 1; i <= count; i++ {
		g := uint64(i)
		m.values[g] = &localGeneration{value: GenerationProvider{Generation: g, SpecificationDigest: strings.Repeat("a", 64), Provider: &fakeProvider{}}, state: "ready"}
	}
	pin := uint64(1)
	m.target = sandbox.NodeDeployment{Generation: uint64(count), SpecificationDigest: strings.Repeat("a", 64), ServingGeneration: &pin}
	return m
}

func TestSparseGenerationControlHasNoLifetimeLimit(t *testing.T) {
	for _, count := range []int{9, 17, 257} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			m := generationFixture(count)
			statuses, retained := map[uint64]bool{}, map[uint64]bool{}
			cursor := uint64(0)
			for i := 0; i < count; i++ {
				batch := m.Statuses()
				if len(batch) > 8 || len(batch) < 2 || batch[0].Generation != uint64(count) || batch[1].Generation != 1 {
					t.Fatal("unbounded batch or missing priorities", batch)
				}
				for _, item := range batch {
					statuses[item.Generation] = true
				}
				refs := m.Retained(cursor)
				if len(refs) > 8 {
					t.Fatal("unbounded retention batch")
				}
				for _, ref := range refs {
					retained[ref.Generation] = true
				}
				cursor = refs[len(refs)-1].Generation
			}
			if len(statuses) != count || len(retained) != count || len(m.values) != count {
				t.Fatal("sparse rotation lost generations", len(statuses), len(retained), len(m.values))
			}
		})
	}
}

func TestRetentionReplyMustMatchWholePendingExchange(t *testing.T) {
	for _, mutation := range []string{"id", "sequence", "connection", "epoch", "partial", "reordered", "digest"} {
		t.Run(mutation, func(t *testing.T) {
			m := generationFixture(3)
			a := &agent{config: AgentConfig{Generations: m}}
			c := &agentConnection{id: uuid.NewString(), epoch: 7}
			refs := m.Retained(0)
			c.pending = &generationControl{ID: uuid.NewString(), Sequence: 1, ConnectionID: c.id, OwnerEpoch: c.epoch, References: refs}
			reply := &generationControl{ID: c.pending.ID, Sequence: 1, ConnectionID: c.id, OwnerEpoch: c.epoch}
			for _, ref := range refs {
				reply.Retentions = append(reply.Retentions, sandbox.GenerationRetention{GenerationReference: ref, Keep: false})
			}
			switch mutation {
			case "id":
				reply.ID = uuid.NewString()
			case "sequence":
				reply.Sequence++
			case "connection":
				reply.ConnectionID = uuid.NewString()
			case "epoch":
				reply.OwnerEpoch++
			case "partial":
				reply.Retentions = reply.Retentions[:1]
			case "reordered":
				reply.Retentions[0], reply.Retentions[1] = reply.Retentions[1], reply.Retentions[0]
			case "digest":
				reply.Retentions[0].SpecificationDigest = strings.Repeat("b", 64)
			}
			work := make(chan []sandbox.GenerationRetention, 1)
			f := frame{Control: reply, Deployment: &m.target}
			if err := a.acceptRetention(c, f, work); err == nil {
				t.Fatal("invalid grant accepted")
			}
			if len(work) != 0 || len(m.values) != 3 || c.pending == nil {
				t.Fatal("invalid exchange authorized partial collection")
			}
		})
	}
	m := generationFixture(3)
	a := &agent{config: AgentConfig{Generations: m}}
	c := &agentConnection{id: uuid.NewString(), epoch: 7}
	refs := m.Retained(0)
	c.pending = &generationControl{ID: uuid.NewString(), Sequence: 1, ConnectionID: c.id, OwnerEpoch: c.epoch, References: refs}
	reply := &generationControl{ID: c.pending.ID, Sequence: 1, ConnectionID: c.id, OwnerEpoch: c.epoch}
	for _, ref := range refs {
		reply.Retentions = append(reply.Retentions, sandbox.GenerationRetention{GenerationReference: ref})
	}
	work := make(chan []sandbox.GenerationRetention, 1)
	f := frame{Control: reply, Deployment: &m.target}
	if err := a.acceptRetention(c, f, work); err != nil {
		t.Fatal(err)
	}
	if err := a.acceptRetention(c, f, work); err == nil {
		t.Fatal("replayed grant accepted")
	}
	if len(work) != 1 {
		t.Fatal("grant queued more than once")
	}
}

type helperOwnedProvider struct {
	*fakeProvider
	caller *microsandbox.ProcessCaller
}

func (p *helperOwnedProvider) Quiescent() bool { return p.caller.Quiescent() }

func TestGrantedDropWaitsForReferencesAndActualHelperExit(t *testing.T) {
	root := t.TempDir()
	helper := filepath.Join(root, "helper")
	started := filepath.Join(root, "started")
	release := filepath.Join(root, "release")
	artifact := filepath.Join(root, "runtime")
	// A local test helper uses files only as deterministic process barriers. Its
	// response waiter returns on cancellation while the actual process stays alive.
	script := "#!/bin/sh\ncat >/dev/null\nprintf started > '" + started + "'\nwhile [ ! -f '" + release + "' ]; do sleep 0.01; done\nprintf '{}\\n'\n"
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("retained bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	caller := &microsandbox.ProcessCaller{}
	defer func() { _ = os.WriteFile(release, nil, 0600); wait(t, caller.Quiescent) }()
	m := generationFixture(3)
	m.target.ServingGeneration = nil
	m.values[1].value.Provider = &helperOwnedProvider{fakeProvider: &fakeProvider{}, caller: caller}
	m.options.Remove = func(context.Context, GenerationProvider) error { return os.Remove(artifact) }
	_, _, unref, err := m.Acquire(1)
	if err != nil {
		t.Fatal(err)
	}
	grant := sandbox.GenerationRetention{GenerationReference: sandbox.GenerationReference{Generation: 1, SpecificationDigest: strings.Repeat("a", 64)}}
	if err := m.Drop(t.Context(), grant); !errors.Is(err, ErrUnavailable) {
		t.Fatal("queued reference did not retain bytes", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := caller.Call(ctx, microsandbox.Request{Config: microsandbox.Config{HelperPath: helper}})
		done <- err
	}()
	wait(t, func() bool { _, err := os.Stat(started); return err == nil })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unref()
	if caller.Quiescent() {
		t.Fatal("caller cancellation pretended helper exited")
	}
	if err := m.Drop(t.Context(), grant); !errors.Is(err, ErrUnavailable) {
		t.Fatal("live helper did not retain generation", err)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Fatal("granted drop removed live helper bytes", err)
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !caller.Quiescent() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !caller.Quiescent() {
		t.Fatal("actual helper Wait did not settle")
	}
	if err := m.Drop(t.Context(), grant); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(artifact); !os.IsNotExist(err) {
		t.Fatal("settled unreferenced generation was not collected", err)
	}
}

func TestCanceledConnectionCannotBeginGenerationCollection(t *testing.T) {
	m := generationFixture(3)
	m.target.ServingGeneration = nil
	removed := false
	m.options.Remove = func(context.Context, GenerationProvider) error { removed = true; return nil }
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	grant := sandbox.GenerationRetention{GenerationReference: sandbox.GenerationReference{Generation: 1, SpecificationDigest: strings.Repeat("a", 64)}}
	if err := m.Drop(ctx, grant); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if removed || m.values[1] == nil {
		t.Fatal("canceled connection began collection")
	}
}

func TestRestartRecoversExactOlderGenerationBeforeAdvertisingReadiness(t *testing.T) {
	digest := strings.Repeat("a", 64)
	requested := make(chan uint64, 1)
	download := make(chan struct{})
	probe := make(chan struct{}, 1)
	qualify := make(chan struct{})
	m, err := NewGenerationManager(t.Context(), GenerationManagerOptions{
		Initial: []GenerationProvider{{Generation: 2, SpecificationDigest: digest, Provider: &fakeProvider{}, Probe: func(context.Context) error { return nil }}},
		Recover: []sandbox.GenerationReference{{Generation: 1, SpecificationDigest: digest}},
		Prepare: func(ctx context.Context, generation uint64, got string) (GenerationProvider, error) {
			if got != digest {
				return GenerationProvider{}, sandbox.ErrOwnership
			}
			requested <- generation
			select {
			case <-download:
			case <-ctx.Done():
				return GenerationProvider{}, ctx.Err()
			}
			return GenerationProvider{Generation: generation, SpecificationDigest: got, Provider: &fakeProvider{}, Probe: func(ctx context.Context) error {
				select {
				case probe <- struct{}{}:
				default:
				}
				select {
				case <-qualify:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}, nil
		},
		Remove: func(context.Context, GenerationProvider) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	pin := uint64(2)
	m.Deployment(sandbox.NodeDeployment{Generation: 2, SpecificationDigest: digest, ServingGeneration: &pin})
	select {
	case generation := <-requested:
		if generation != 1 {
			t.Fatal("replaced old placement generation", generation)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("old generation was never recovered")
	}
	if m.Ready(1) {
		t.Fatal("missing payload advertised readiness")
	}
	if err := m.Drop(t.Context(), sandbox.GenerationRetention{GenerationReference: sandbox.GenerationReference{Generation: 1, SpecificationDigest: digest}}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("collection entered active repair", err)
	}
	close(download)
	select {
	case <-probe:
	case <-time.After(4 * time.Second):
		t.Fatal("restored provider never qualified")
	}
	if m.Ready(1) {
		t.Fatal("file presence advertised provider readiness")
	}
	close(qualify)
	wait(t, func() bool { return m.Ready(1) })
	provider, ready, release, err := m.Acquire(1)
	if err != nil || !ready || provider == nil {
		t.Fatal("old generation not usable after actual qualification", err)
	}
	release()
}

func TestGenerationDeploymentRejectsConflictingImmutableIdentity(t *testing.T) {
	m := generationFixture(3)
	bad := m.target
	bad.SpecificationDigest = strings.Repeat("b", 64)
	if err := m.Deployment(bad); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatal("conflicting target accepted", err)
	}
	if m.target.SpecificationDigest != strings.Repeat("a", 64) || m.values[3].value.SpecificationDigest != m.target.SpecificationDigest {
		t.Fatal("rejected metadata changed provider identity")
	}
	bad = m.target
	future := uint64(4)
	bad.ServingGeneration = &future
	if err := m.Deployment(bad); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatal("future serving pin accepted", err)
	}
}

func TestInterruptedCollectionNeverPreparesOrServesAfterRestart(t *testing.T) {
	digest := strings.Repeat("a", 64)
	ref := sandbox.GenerationReference{Generation: 1, SpecificationDigest: digest}
	probed := make(chan struct{}, 1)
	removes := 0
	manager, err := NewGenerationManager(t.Context(), GenerationManagerOptions{
		Initial: []GenerationProvider{{Generation: 2, SpecificationDigest: digest, Provider: &fakeProvider{}, Probe: func(context.Context) error {
			select {
			case probed <- struct{}{}:
			default:
			}
			return nil
		}}},
		Collect: []sandbox.GenerationReference{ref},
		Prepare: func(context.Context, uint64, string) (GenerationProvider, error) {
			t.Error("collection entered preparation")
			return GenerationProvider{}, sandbox.ErrInvalid
		},
		Remove: func(_ context.Context, value GenerationProvider) error {
			removes++
			if value.Generation != 1 {
				t.Error("wrong generation")
			}
			if removes == 1 {
				return errors.New("interrupted cleanup")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	pin := uint64(2)
	if err = manager.Deployment(sandbox.NodeDeployment{Generation: 2, SpecificationDigest: digest, ServingGeneration: &pin}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-probed:
	case <-time.After(3 * time.Second):
		t.Fatal("provider loop did not run")
	}
	if _, _, _, err = manager.Acquire(1); err == nil {
		t.Fatal("unfinished collection admitted an operation")
	}
	for _, status := range manager.Statuses() {
		if status.Generation == 1 {
			t.Fatal("unfinished collection advertised readiness", status)
		}
	}
	if len(manager.Retained(0)) != 2 || removes != 0 {
		t.Fatal("restart consumed old drop authorization")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	grant := sandbox.GenerationRetention{GenerationReference: ref}
	if err = manager.Drop(cancelled, grant); err == nil || removes != 0 {
		t.Fatal("cancelled connection collected")
	}
	if err = manager.Drop(t.Context(), grant); err == nil {
		t.Fatal("expected interrupted cleanup")
	}
	if _, _, _, err = manager.Acquire(1); err == nil {
		t.Fatal("failed cleanup resumed serving")
	}
	if err = manager.Drop(t.Context(), grant); err != nil {
		t.Fatal(err)
	}
	if len(manager.Retained(0)) != 1 || removes != 2 {
		t.Fatal("cleanup did not settle")
	}
}

func TestPreparationDiagnosticPreservesTypedCause(t *testing.T) {
	for _, cause := range []error{sandbox.ErrRuntimeDownloadFailed, sandbox.ErrDockerUnavailable, sandbox.ErrKVMUnavailable, sandbox.ErrOwnership, context.Canceled, errors.New("raw secret provider text")} {
		t.Run(sandbox.NodeDiagnostic(cause)+cause.Error(), func(t *testing.T) {
			m, err := NewGenerationManager(t.Context(), GenerationManagerOptions{
				Prepare: func(context.Context, uint64, string) (GenerationProvider, error) { return GenerationProvider{}, cause },
				Remove:  func(context.Context, GenerationProvider) error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			if err := m.Deployment(sandbox.NodeDeployment{Generation: 1, SpecificationDigest: strings.Repeat("a", 64)}); err != nil {
				t.Fatal(err)
			}
			wait(t, func() bool { s := m.Statuses(); return len(s) == 1 && s[0].State == "failed" })
			if got := m.Statuses()[0].Diagnostic; got != sandbox.NodeDiagnostic(cause) {
				t.Fatal("wrong fixed diagnostic", got)
			}
		})
	}
}
