package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/workspacepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspaces"
)

// fixtureWorkspaceInstallation seals workspace configurations in the tests; the
// fixture adapter keeps no credential.
const fixtureWorkspaceInstallation = "fixture-installation"

// fixtureWorkspaces builds the workspace execution operations on lease as
// cmd/server does, with the fixture adapter in place of the registered ones so
// no test needs NFS.
func fixtureWorkspaces(s *Store, lease *pgunit.Lease) *workspaces.ExecutionOperations {
	return workspaces.NewExecution(workspacepg.New(s.pooled, s.credentialCipher, fixtureWorkspaceInstallation),
		workspacepg.NewExecution(lease, s.credentialCipher, fixtureWorkspaceInstallation), fixtureControls{}, lease)
}

// selectWorkspaceStorage selects the fixture adapter on operations as an operator
// does before Web setup.
func selectWorkspaceStorage(t testing.TB, operations *workspaces.ExecutionOperations) {
	t.Helper()
	selection := workspaces.SelectionInput{Selection: workspaces.Selection{Configuration: workspacefs.Configuration{ID: uuid.NewString(), Adapter: "fixture", Parameters: json.RawMessage(`{}`)}}}
	ctx := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", RequestID: uuid.NewString(), TraceID: uuid.NewString()})
	if _, err := operations.Configure(ctx, selection, func(workspacefs.Declaration) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

// fixtureControls constructs the fixture adapter for every configuration.
type fixtureControls struct{}

func (fixtureControls) Normalize(c workspacefs.Configuration, _ json.RawMessage) (workspacefs.ConfigurationRecord, error) {
	return workspacefs.ConfigurationRecord{Configuration: c}, nil
}

func (fixtureControls) Control(c workspacefs.ConfigurationRecord) (workspacefs.Control, error) {
	return fixtureControl{configuration: c.ID}, nil
}

// fixtureControl declares what microsandbox requires and issues an attachment
// for every Reference; Core never resolves paths.
type fixtureControl struct{ configuration string }

func (fixtureControl) Declaration() workspacefs.Declaration {
	return workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true}
}
func (fixtureControl) Check(context.Context) error { return nil }
func (c fixtureControl) Create(_ context.Context, r workspacefs.Reference, _ uint32) (workspacefs.Attachment, error) {
	return workspacefs.Attachment{Reference: r, ConfigurationID: c.configuration, Kind: workspacefs.AttachmentHostDirectory, Native: json.RawMessage(`{}`)}, nil
}
func (fixtureControl) Delete(context.Context, workspacefs.Reference) error { return nil }
