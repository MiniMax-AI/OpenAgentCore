package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	"github.com/google/uuid"
)

func TestE2BReplacementVerifiesTwiceAndNeverPublishesFailedCommit(t *testing.T) {
	_, owner, deployments, reader := resetManagerStore(t)
	id := uuid.NewString()
	if err := owner.Deployment.Claim(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	input := sandbox.Selection{Provider: "e2b", Configuration: &e2b.DeploymentConfiguration{APIKey: "old-key", Template: "runtime:" + uuid.NewString()}}
	input.Resources.CPUs = 2
	input.Resources.MemoryMiB = 2048
	if _, err := owner.Deployment.Initialize(t.Context(), id, input); err != nil {
		t.Fatal(err)
	}
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	provider := hub.Proxy(uuid.NewString(), "docker", docker.Operations(), 1)
	verifyCalls, published, fenced, released := 0, 0, 0, 0
	var rejectAt int
	var rejection error = sandbox.ErrCredentialOwnership
	config := NewDeferredRuntimeProvider(id, func(ctx context.Context) (*RuntimeProvider, error) {
		setup, err := deployments.Setup(ctx)
		if err != nil {
			return nil, err
		}
		return &RuntimeProvider{InstallationID: id, ProviderKind: "e2b", Mode: "direct", Generation: setup.Generation, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: provider}, nil
	}, func(ctx context.Context, setup deployment.Setup) (PreparedRuntimeDeployment, error) {
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
	m, err := newRuntimeManager(owner, deployments, reader, runtimegateway.NewRegistry(), config)
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
	input.Configuration.(*e2b.DeploymentConfiguration).APIKey = "candidate-key"
	request := input
	request.ExpectedGeneration = 1
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
			rejection = &deployment.ResetRequiredError{CurrentProvider: "e2b", RequestedProvider: "e2b"}
		case "unknown ownership":
			rejectAt = 1
			rejection = sandbox.ErrConfigurationUnconfirmed
		case "commit":
			ctx = t.Context()
		}
		if _, err := worker.UpdateSandboxDeployment(ctx, request); err == nil {
			t.Fatal("failure published", failure)
		}
		committed, err := deployments.Setup(t.Context())
		if err != nil || committed.Generation != 1 || committed.Configuration.(*e2b.DeploymentConfiguration).APIKey != "old-key" || published != 0 || fenced != released {
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
		var stale *deployment.GenerationStaleError
		if !errors.As(err, &stale) {
			t.Fatal(err)
		}
	}
	if verifyCalls != before {
		t.Fatal("stale request reached E2B")
	}
}
