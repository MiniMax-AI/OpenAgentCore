package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/adminaudit"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/e2b"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/node"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestE2BReplacementVerifiesTwiceAndNeverPublishesFailedCommit(t *testing.T) {
	s, lease := resetManagerStore(t)
	writer := lease.Store()
	id := uuid.NewString()
	if err := writer.ClaimWebSandboxDeployment(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	input := store.SandboxDeploymentSetupRequest{Provider: "e2b", E2B: &store.SandboxE2BConfiguration{APIKey: "old-key", Template: "runtime:" + uuid.NewString()}}
	input.Resources.CPUs = 2
	input.Resources.MemoryMiB = 2048
	if _, err := writer.InitializeSandboxDeployment(t.Context(), id, input); err != nil {
		t.Fatal(err)
	}
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	provider := hub.Proxy(uuid.NewString(), "docker", 1)
	verifyCalls, published, fenced, released := 0, 0, 0, 0
	var rejectAt int
	var rejection error = e2b.ErrTeamMismatch
	config := NewDeferredRuntimeProvider(id, func(ctx context.Context) (*RuntimeProvider, error) {
		setup, err := s.GetSandboxSetup(ctx)
		if err != nil {
			return nil, err
		}
		return &RuntimeProvider{InstallationID: id, ProviderKind: "e2b", Mode: "direct", Generation: setup.Generation, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: provider}, nil
	}, func(ctx context.Context, setup store.SandboxSetup) (PreparedRuntimeDeployment, error) {
		return PreparedRuntimeDeployment{Config: &RuntimeProvider{InstallationID: id, ProviderKind: "e2b", Mode: "direct", CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: provider},
			VerifyCredential: func(context.Context) error {
				verifyCalls++
				if verifyCalls == rejectAt {
					return rejection
				}
				return nil
			},
			FenceCredential: func(context.Context) (func(), error) { fenced++; return func() { released++ }, nil }, Publish: func(*RuntimeProvider) { published++ }}, nil
	})
	m, err := newRuntimeManager(writer, gateway.NewRegistry(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.stop(); m.drain() }()
	if _, err = m.ensureDeployment(t.Context()); err != nil {
		t.Fatal(err)
	}
	old, err := m.node("")
	if err != nil {
		t.Fatal(err)
	}
	worker := &Worker{runtimes: m}
	input.E2B.APIKey = "candidate-key"
	request := store.SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: input, ExpectedGeneration: 1}
	audit := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "test", RequestID: "test", TraceID: "test"})
	for _, failure := range []string{"preliminary", "final", "unanchored legacy or revoked committed key", "unknown ownership", "commit"} {
		verifyCalls = 0
		rejectAt = 0
		ctx := audit
		switch failure {
		case "preliminary":
			rejectAt = 1
		case "final":
			rejectAt = 2
		case "unanchored legacy or revoked committed key":
			rejectAt = 1
			rejection = &store.SandboxResetRequiredError{CurrentProvider: "e2b", RequestedProvider: "e2b"}
		case "unknown ownership":
			rejectAt = 1
			rejection = e2b.ErrRequestUnconfirmed
		case "commit":
			ctx = t.Context()
		}
		if _, err := worker.UpdateSandboxDeployment(ctx, request); err == nil {
			t.Fatal("failure published", failure)
		}
		committed, err := s.GetSandboxSetup(t.Context())
		if err != nil || committed.Generation != 1 || committed.E2B.APIKey != "old-key" || published != 0 || fenced != released {
			t.Fatal("partial credential publication", failure, err)
		}
		current, err := m.node("")
		if err != nil || current != old || m.switching {
			t.Fatal("online failure drained an owned lifecycle", err)
		}
	}
	verifyCalls = 0
	rejectAt = 0
	result, err := worker.UpdateSandboxDeployment(audit, request)
	if err != nil || result.Generation != 2 || verifyCalls != 2 || published != 1 || fenced != released {
		t.Fatal(result, verifyCalls, published, err)
	}
	current, err := m.node("")
	if err != nil || current != old {
		t.Fatal("online commit replaced lifecycle", err)
	}
	before := verifyCalls
	if _, err := worker.UpdateSandboxDeployment(audit, request); err == nil {
		t.Fatal("stale replay accepted")
	} else {
		var stale *store.SandboxGenerationStaleError
		if !errors.As(err, &stale) {
			t.Fatal(err)
		}
	}
	if verifyCalls != before {
		t.Fatal("stale request reached E2B")
	}
}
