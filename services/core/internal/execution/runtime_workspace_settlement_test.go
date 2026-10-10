package execution

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/projectpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/workspacepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspaces"
	"github.com/google/uuid"
)

// settledWorkspaceRefusal models the provider's pre-native filesystem rejection:
// an absent settled receipt proves that no compute mutation needs observation.
type settledWorkspaceRefusal struct {
	sandbox.SandboxProvider
	creates int
}

func (p *settledWorkspaceRefusal) Create(_ context.Context, q sandbox.Bootstrap) (sandbox.Info, error) {
	p.creates++
	return sandbox.Info{Reference: q.Reference, State: "absent", CreateSettled: true}, workspacefs.ErrUnavailable
}

type settlementWorkspaceControl struct {
	workspacefs.Control
	configuration workspacefs.Configuration
	deletes       int
}

func (c *settlementWorkspaceControl) Declaration() workspacefs.Declaration {
	return workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory}
}
func (c *settlementWorkspaceControl) Create(_ context.Context, ref workspacefs.Reference) (workspacefs.Attachment, error) {
	return workspacefs.Attachment{Reference: ref, ConfigurationID: c.configuration.ID, Kind: workspacefs.AttachmentHostDirectory, Native: []byte(`{}`)}, nil
}
func (c *settlementWorkspaceControl) Delete(context.Context, workspacefs.Reference) error {
	c.deletes++
	return nil
}

type settlementWorkspaceControls struct{ control *settlementWorkspaceControl }

func (c settlementWorkspaceControls) Control(workspacefs.Configuration) (workspacefs.Control, error) {
	return c.control, nil
}
func (c settlementWorkspaceControls) Normalize(config workspacefs.Configuration) (workspacefs.Configuration, error) {
	return config, nil
}

func TestSettledWorkspaceRefusalRetainsReceiptUntilExplicitSessionDeletion(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	lease, err := pgunit.AcquireLease(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	deployments, reader, operations := testDeployment(t, pool, pgtest.CredentialKey(t), lease)
	owner := Owner{Lease: lease, Deployment: operations, Sessions: sessionExecution(t, lease)}
	installation := initializeE2BDeployment(t, owner)
	projectID := uuid.NewString()
	audit := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", ProjectID: projectID, RequestID: uuid.NewString(), TraceID: uuid.NewString()})
	management, err := projects.NewService(projectpg.New(pgunit.NewPool(pool)))
	if err != nil {
		t.Fatal(err)
	}
	project, err := management.CreateProject(audit, projects.CreateProject{ID: projectID, Name: "Workspace settlement"})
	if err != nil {
		t.Fatal(err)
	}
	sessionReader, service := testSessions(t, pool, pgtest.CredentialKey(t))
	created, err := service.CreateSession(t.Context(), project.TenantID, sessions.CreateSession{Creator: identity.Subject{Kind: "service_account", ID: "fixture"}, Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"test-model"},"environment":{"type":"openai_hosted","network":{"access":"disabled"}}}`), ModelProvider: &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://model.fixture.example/v1", APIKey: "fixture-key"}, ModelProviderSource: v1.ExecutionSourceSession})
	if err != nil {
		t.Fatal(err)
	}
	session := created.Session
	configuration := workspacefs.Configuration{ID: uuid.NewString(), Adapter: "fixture", Parameters: []byte(`{}`)}
	storage := workspacepg.New(pgunit.NewPool(pool))
	writer := workspacepg.NewExecution(lease)
	workspaceAudit := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", RequestID: uuid.NewString(), TraceID: uuid.NewString()})
	if err = writer.SelectConfiguration(workspaceAudit, configuration); err != nil {
		t.Fatal(err)
	}
	control := &settlementWorkspaceControl{configuration: configuration}
	filesystems := workspaces.NewExecution(storage, writer, settlementWorkspaceControls{control}, lease)
	provider := &settledWorkspaceRefusal{}
	declaration := control.Declaration()
	lifecycle := &runtimeLifecycle{sessions: sessionReader, sessionExecution: owner.Sessions, deployment: operations, deployments: deployments, reader: reader, lease: lease, workspaces: filesystems, config: RuntimeProvider{InstallationID: installation, Provider: provider, Workspace: &declaration, WorkspaceRequirements: &workspacefs.Requirements{Attachment: workspacefs.AttachmentHostDirectory}}}
	allocation, err := lifecycle.provision(t.Context(), project.TenantID, session.Environment.ID, installation)
	if !errors.Is(err, workspacefs.ErrUnavailable) || allocation.State != "released" || !allocation.CreateSettled {
		t.Fatalf("settled refusal did not persist release: %+v, %v", allocation, err)
	}
	environment, err := sessionReader.GetEnvironment(t.Context(), project.TenantID, session.Environment.ID)
	if err != nil || environment.Status != "failed" {
		t.Fatal("missing durable failure", environment.Status, err)
	}
	replay, err := lifecycle.provision(t.Context(), project.TenantID, session.Environment.ID, installation)
	if err != nil || !replay.Replayed || replay.ID != allocation.ID || provider.creates != 1 {
		t.Fatal("settled failed creation retried compute", replay, err)
	}
	if _, err = filesystems.DeleteBatch(t.Context(), ""); err != nil || control.deletes != 0 {
		t.Fatal("failure implicitly deleted retained filesystem", err)
	}
	if err = service.DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: project.TenantID, SessionID: session.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = filesystems.DeleteBatch(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	retained, err := storage.Get(t.Context(), project.TenantID, session.Environment.ID)
	if err != nil || retained.State != workspaces.Deleted || retained.Attachment == nil || control.deletes != 1 {
		t.Fatal("explicit Session deletion did not settle filesystem cleanup", retained, err)
	}
	persisted, err := reader.EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: project.TenantID, EnvironmentID: session.Environment.ID})
	if err != nil || persisted.ID != allocation.ID || persisted.State != "released" || !persisted.CreateSettled {
		t.Fatal("cleanup lost immutable compute receipt", persisted, err)
	}
}
