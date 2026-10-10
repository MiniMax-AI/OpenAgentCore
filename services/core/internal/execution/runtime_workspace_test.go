package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspaces"
	"github.com/google/uuid"
)

type workspaceEnvironmentReader struct{ sessions.Reader }

func (workspaceEnvironmentReader) GetSession(context.Context, string, string) (sessions.Session, error) {
	return sessions.Session{Engine: "codex"}, nil
}

func (workspaceEnvironmentReader) GetEnvironment(context.Context, string, string) (sessions.Environment, error) {
	return sessions.Environment{Configuration: []byte(`{"type":"openai_hosted"}`)}, nil
}

type executionWorkspaceFixture struct {
	workspaces.Storage
	workspaces.ExecutionStorage
	workspacefs.Control
	record                  workspaces.Record
	creates, deletes, binds int
	failure                 error
}

func (f *executionWorkspaceFixture) Bind(context.Context, string, string) (workspaces.Record, error) {
	f.binds++
	return f.record, nil
}
func (f *executionWorkspaceFixture) ControlForConfiguration(workspacefs.Configuration) (workspacefs.Control, error) {
	return f, nil
}
func (f *executionWorkspaceFixture) Declaration() workspacefs.Declaration {
	return workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory}
}
func (f *executionWorkspaceFixture) Create(context.Context, workspacefs.Reference) (workspacefs.Attachment, error) {
	f.creates++
	return workspacefs.Attachment{Reference: f.record.Reference, ConfigurationID: f.record.Configuration.ID, Kind: workspacefs.AttachmentHostDirectory, Native: []byte(`{}`)}, f.failure
}
func (f *executionWorkspaceFixture) MarkReady(_ context.Context, _ workspacefs.Reference, a workspacefs.Attachment) error {
	f.record.State = workspaces.Ready
	f.record.Attachment = &a
	return nil
}
func (f *executionWorkspaceFixture) DeletionCandidates(context.Context, string) ([]workspaces.Record, error) {
	return []workspaces.Record{f.record}, nil
}
func (f *executionWorkspaceFixture) BeginDelete(context.Context, workspacefs.Reference) error {
	f.record.State = workspaces.Deleting
	return nil
}
func (f *executionWorkspaceFixture) Delete(context.Context, workspacefs.Reference) error {
	f.deletes++
	return nil
}
func (f *executionWorkspaceFixture) MarkDeleted(context.Context, workspacefs.Reference) error {
	f.record.State = workspaces.Deleted
	return nil
}

type workspaceControls struct{ fixture *executionWorkspaceFixture }

func (c workspaceControls) Normalize(configuration workspacefs.Configuration) (workspacefs.Configuration, error) {
	return configuration, nil
}

func (c workspaceControls) Control(config workspacefs.Configuration) (workspacefs.Control, error) {
	return c.fixture.ControlForConfiguration(config)
}
func newExecutionWorkspaceFixture() *executionWorkspaceFixture {
	return &executionWorkspaceFixture{record: workspaces.Record{Reference: workspacefs.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), ObjectID: uuid.NewString()}, Configuration: workspacefs.Configuration{ID: uuid.NewString(), Adapter: "fixture", Parameters: []byte(`{}`)}, State: workspaces.Creating}}
}

func TestRuntimeWorkspaceConvergesBeforeComputeReservation(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "unavailable"}[fail], func(t *testing.T) {
			f := newExecutionWorkspaceFixture()
			if fail {
				f.failure = workspacefs.ErrUnavailable
			}
			reserved := false
			stop := errors.New("reservation reached")
			_, operations := deploymentOperations(t, &strictDeploymentStorage{t: t}, &strictDeploymentReader{t: t}, &strictExecutionStorage{t: t, withReservation: func(context.Context, deployment.AllocationKey, func(sessions.LockedSession, deployment.ReservationTx) error) error {
				reserved = true
				if f.record.State != workspaces.Ready || f.creates != 1 {
					t.Fatal("compute reserved before filesystem converged")
				}
				return stop
			}})
			r := &runtimeLifecycle{sessions: workspaceEnvironmentReader{}, deployment: operations, reader: &strictDeploymentReader{t: t, environmentAllocation: func(context.Context, deployment.AllocationKey) (deployment.Allocation, error) {
				return deployment.Allocation{}, deployment.ErrNotFound
			}}, workspaces: workspaces.NewExecution(f, f, workspaceControls{f}, heldLease{}), config: RuntimeProvider{InstallationID: uuid.NewString(), Workspace: &workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory}, WorkspaceRequirements: &workspacefs.Requirements{Attachment: workspacefs.AttachmentHostDirectory}}}
			_, err := r.provision(t.Context(), f.record.Reference.TenantID, f.record.Reference.EnvironmentID, r.config.InstallationID)
			if fail {
				if !errors.Is(err, workspacefs.ErrUnavailable) || reserved {
					t.Fatal("unavailable storage admitted compute", err)
				}
			} else if !errors.Is(err, stop) || !reserved {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeWorkspaceScanRunsWithoutSandboxOrNodes(t *testing.T) {
	f := newExecutionWorkspaceFixture()
	m, err := newRuntimeManager(Owner{Lease: heldLease{}, Workspaces: workspaces.NewExecution(f, f, workspaceControls{f}, heldLease{})}, nil, nil, nil, runtimegateway.NewRegistry(), unusedRuntimes(t))
	if err != nil {
		t.Fatal(err)
	}
	defer m.stop()
	if err = m.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.deletes != 1 || f.record.State != workspaces.Deleted {
		t.Fatal("unconfigured Sandbox prevented workspace deletion")
	}
}

func (f *executionWorkspaceFixture) ActiveConfiguration(context.Context) (workspacefs.Configuration, error) {
	return f.record.Configuration, nil
}
func (f *executionWorkspaceFixture) SelectConfiguration(_ context.Context, c workspacefs.Configuration) error {
	f.record.Configuration = c
	return nil
}
func (f *executionWorkspaceFixture) Check(context.Context) error { return f.failure }

func TestWorkspaceSelectionDerivesBeforeDeploymentValidation(t *testing.T) {
	f := newExecutionWorkspaceFixture()
	m := &runtimeManager{workspaces: workspaces.NewExecution(f, f, workspaceControls{f}, heldLease{})}
	selection, err := m.workspaceSelection(t.Context(), sandbox.Selection{})
	if err != nil || selection.Workspace == nil || selection.Workspace.Attachment != workspacefs.AttachmentHostDirectory {
		t.Fatal("missing derived generation declaration", err)
	}
	selection.Workspace.UserXAttr = true
	if _, err = m.workspaceSelection(t.Context(), selection); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("caller could forge declaration", err)
	}
}

func TestOwnedGenerationDoesNotAdoptLaterFilesystemSelection(t *testing.T) {
	f := newExecutionWorkspaceFixture()
	stop := errors.New("reservation reached")
	_, operations := deploymentOperations(t, &strictDeploymentStorage{t: t}, &strictDeploymentReader{t: t}, &strictExecutionStorage{t: t, withReservation: func(context.Context, deployment.AllocationKey, func(sessions.LockedSession, deployment.ReservationTx) error) error {
		return stop
	}})
	r := &runtimeLifecycle{sessions: workspaceEnvironmentReader{}, deployment: operations, workspaces: workspaces.NewExecution(f, f, workspaceControls{f}, heldLease{}), config: RuntimeProvider{InstallationID: uuid.NewString()}}
	_, err := r.provision(t.Context(), f.record.Reference.TenantID, f.record.Reference.EnvironmentID, r.config.InstallationID)
	if !errors.Is(err, stop) || f.binds != 0 || f.creates != 0 {
		t.Fatal("owned generation retroactively attached filesystem", err)
	}
}

func TestConfigureWorkspaceBeforeSandboxSetup(t *testing.T) {
	f := newExecutionWorkspaceFixture()
	reader := &strictDeploymentReader{t: t, deployment: func(context.Context) (deployment.Record, error) {
		return deployment.Record{InstallationID: uuid.NewString(), Specification: []byte(`{}`)}, nil
	}}
	service, _ := deploymentOperations(t, &strictDeploymentStorage{t: t}, reader, &strictExecutionStorage{t: t})
	m, err := newRuntimeManager(Owner{Lease: heldLease{}, Workspaces: workspaces.NewExecution(f, f, workspaceControls{f}, heldLease{})}, service, reader, nil, runtimegateway.NewRegistry(), unusedRuntimes(t))
	if err != nil {
		t.Fatal(err)
	}
	defer m.stop()
	worker := &Worker{runtimes: m}
	next := f.record.Configuration
	next.ID = uuid.NewString()
	got, err := worker.Configure(t.Context(), next)
	if err != nil || got.ID != next.ID {
		t.Fatal("filesystem selection requires sandbox setup", err)
	}
}
