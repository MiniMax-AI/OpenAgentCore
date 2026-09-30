package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmenttemplates"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimehistory"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
)

// Strict fakes: one per Dependencies area, with a func field per method. A
// test sets only the funcs it expects; calling any other method fails the test.

// unexpectedCall fails the test and panics. net/http recovers the panic on an
// httptest server goroutine, where t.Fatalf cannot stop the test, and a direct
// ServeHTTP call fails loudly.
func unexpectedCall(t testing.TB, method string) {
	t.Helper()
	t.Errorf("unexpected call to %s", method)
	panic("unexpected call to " + method)
}

type fakeAdmin struct {
	t                       testing.TB
	readAdminSummary        func(context.Context, string, store.AdminSummaryFilter, func(sessions.Session, *string) error) (store.AdminAssetCounts, error)
	listAdminRuntimeTargets func(context.Context, []string, string, int, bool) (store.AdminRuntimeTargetPage, error)
}

func (f *fakeAdmin) ReadAdminSummary(a0 context.Context, a1 string, a2 store.AdminSummaryFilter, a3 func(sessions.Session, *string) error) (store.AdminAssetCounts, error) {
	if f.readAdminSummary == nil {
		unexpectedCall(f.t, "ReadAdminSummary")
	}
	return f.readAdminSummary(a0, a1, a2, a3)
}

func (f *fakeAdmin) ListAdminRuntimeTargets(a0 context.Context, a1 []string, a2 string, a3 int, a4 bool) (store.AdminRuntimeTargetPage, error) {
	if f.listAdminRuntimeTargets == nil {
		unexpectedCall(f.t, "ListAdminRuntimeTargets")
	}
	return f.listAdminRuntimeTargets(a0, a1, a2, a3, a4)
}

type fakeAdminAudit struct {
	t              testing.TB
	listAdminAudit func(context.Context, adminaudit.Filter) (adminaudit.Page, error)
}

func (f *fakeAdminAudit) ListAdminAudit(a0 context.Context, a1 adminaudit.Filter) (adminaudit.Page, error) {
	if f.listAdminAudit == nil {
		unexpectedCall(f.t, "ListAdminAudit")
	}
	return f.listAdminAudit(a0, a1)
}

type fakeAgents struct {
	t      testing.TB
	create func(context.Context, agents.CreateCommand) (agents.Agent, error)
	update func(context.Context, agents.UpdateCommand) (agents.Agent, error)
	delete func(context.Context, agents.DeleteCommand) (string, error)
}

func (f *fakeAgents) Create(a0 context.Context, a1 agents.CreateCommand) (agents.Agent, error) {
	if f.create == nil {
		unexpectedCall(f.t, "Create")
	}
	return f.create(a0, a1)
}

func (f *fakeAgents) Update(a0 context.Context, a1 agents.UpdateCommand) (agents.Agent, error) {
	if f.update == nil {
		unexpectedCall(f.t, "Update")
	}
	return f.update(a0, a1)
}

func (f *fakeAgents) Delete(a0 context.Context, a1 agents.DeleteCommand) (string, error) {
	if f.delete == nil {
		unexpectedCall(f.t, "Delete")
	}
	return f.delete(a0, a1)
}

type fakeAgentsReader struct {
	t                         testing.TB
	getAgent                  func(context.Context, string, string) (agents.Agent, error)
	listAgents                func(context.Context, agents.ListQuery) (agents.Page, error)
	getAgentWithModelProvider func(context.Context, string, string) (agents.Agent, *v1.ModelProviderInput, error)
}

func (f *fakeAgentsReader) GetAgent(a0 context.Context, a1 string, a2 string) (agents.Agent, error) {
	if f.getAgent == nil {
		unexpectedCall(f.t, "GetAgent")
	}
	return f.getAgent(a0, a1, a2)
}

func (f *fakeAgentsReader) ListAgents(a0 context.Context, a1 agents.ListQuery) (agents.Page, error) {
	if f.listAgents == nil {
		unexpectedCall(f.t, "ListAgents")
	}
	return f.listAgents(a0, a1)
}

func (f *fakeAgentsReader) GetAgentWithModelProvider(a0 context.Context, a1 string, a2 string) (agents.Agent, *v1.ModelProviderInput, error) {
	if f.getAgentWithModelProvider == nil {
		unexpectedCall(f.t, "GetAgentWithModelProvider")
	}
	return f.getAgentWithModelProvider(a0, a1, a2)
}

type fakeArtifacts struct {
	t                     testing.TB
	deleteSessionArtifact func(context.Context, sessions.DeleteSessionArtifactCommand) error
}

func (f *fakeArtifacts) DeleteSessionArtifact(a0 context.Context, a1 sessions.DeleteSessionArtifactCommand) error {
	if f.deleteSessionArtifact == nil {
		unexpectedCall(f.t, "DeleteSessionArtifact")
	}
	return f.deleteSessionArtifact(a0, a1)
}

type fakeArtifactsReader struct {
	t                    testing.TB
	getSessionArtifact   func(context.Context, string, string, string) (sessions.Artifact, error)
	listSessionArtifacts func(context.Context, string, string, string, string, int, bool) (sessions.ArtifactPage, error)
	readSessionArtifact  func(context.Context, string, string, string, func(sessions.Artifact, io.Reader) error) error
}

func (f *fakeArtifactsReader) GetSessionArtifact(a0 context.Context, a1 string, a2 string, a3 string) (sessions.Artifact, error) {
	if f.getSessionArtifact == nil {
		unexpectedCall(f.t, "GetSessionArtifact")
	}
	return f.getSessionArtifact(a0, a1, a2, a3)
}

func (f *fakeArtifactsReader) ListSessionArtifacts(a0 context.Context, a1 string, a2 string, a3 string, a4 string, a5 int, a6 bool) (sessions.ArtifactPage, error) {
	if f.listSessionArtifacts == nil {
		unexpectedCall(f.t, "ListSessionArtifacts")
	}
	return f.listSessionArtifacts(a0, a1, a2, a3, a4, a5, a6)
}

func (f *fakeArtifactsReader) ReadSessionArtifact(a0 context.Context, a1 string, a2 string, a3 string, a4 func(sessions.Artifact, io.Reader) error) error {
	if f.readSessionArtifact == nil {
		unexpectedCall(f.t, "ReadSessionArtifact")
	}
	return f.readSessionArtifact(a0, a1, a2, a3, a4)
}

type fakeConfigurationDiscovery struct {
	t                     testing.TB
	discoverConfiguration func(ctx context.Context, provider string, input sandbox.ConfigurationDiscoveryInput) (json.RawMessage, error)
}

func (f *fakeConfigurationDiscovery) DiscoverConfiguration(a0 context.Context, a1 string, a2 sandbox.ConfigurationDiscoveryInput) (json.RawMessage, error) {
	if f.discoverConfiguration == nil {
		unexpectedCall(f.t, "DiscoverConfiguration")
	}
	return f.discoverConfiguration(a0, a1, a2)
}

type fakeDeployment struct {
	t                   testing.TB
	view                func(context.Context) (deployment.View, error)
	listNodes           func(context.Context) ([]deployment.Node, error)
	nodeDetail          func(context.Context, string, string) (deployment.NodeDetail, error)
	updateNode          func(context.Context, string, deployment.NodeUpdate) error
	removeNode          func(context.Context, string) error
	createEnrollment    func(context.Context, deployment.Capacity) (deployment.EnrollmentToken, error)
	enroll              func(context.Context, string, deployment.Enrollment) (deployment.NodeIdentity, error)
	nodeConfiguration   func(context.Context, string, string, uint64) (deployment.NodeConfiguration, error)
	nodeStatus          func(context.Context, string, string) (deployment.NodeStatus, error)
	decodeConfiguration func(string, json.RawMessage, json.RawMessage) (sandbox.Configuration, error)
}

func (f *fakeDeployment) View(a0 context.Context) (deployment.View, error) {
	if f.view == nil {
		unexpectedCall(f.t, "View")
	}
	return f.view(a0)
}

func (f *fakeDeployment) ListNodes(a0 context.Context) ([]deployment.Node, error) {
	if f.listNodes == nil {
		unexpectedCall(f.t, "ListNodes")
	}
	return f.listNodes(a0)
}

func (f *fakeDeployment) NodeDetail(a0 context.Context, a1 string, a2 string) (deployment.NodeDetail, error) {
	if f.nodeDetail == nil {
		unexpectedCall(f.t, "NodeDetail")
	}
	return f.nodeDetail(a0, a1, a2)
}

func (f *fakeDeployment) UpdateNode(a0 context.Context, a1 string, a2 deployment.NodeUpdate) error {
	if f.updateNode == nil {
		unexpectedCall(f.t, "UpdateNode")
	}
	return f.updateNode(a0, a1, a2)
}

func (f *fakeDeployment) RemoveNode(a0 context.Context, a1 string) error {
	if f.removeNode == nil {
		unexpectedCall(f.t, "RemoveNode")
	}
	return f.removeNode(a0, a1)
}

func (f *fakeDeployment) CreateEnrollment(a0 context.Context, a1 deployment.Capacity) (deployment.EnrollmentToken, error) {
	if f.createEnrollment == nil {
		unexpectedCall(f.t, "CreateEnrollment")
	}
	return f.createEnrollment(a0, a1)
}

func (f *fakeDeployment) Enroll(a0 context.Context, a1 string, a2 deployment.Enrollment) (deployment.NodeIdentity, error) {
	if f.enroll == nil {
		unexpectedCall(f.t, "Enroll")
	}
	return f.enroll(a0, a1, a2)
}

func (f *fakeDeployment) NodeConfiguration(a0 context.Context, a1 string, a2 string, a3 uint64) (deployment.NodeConfiguration, error) {
	if f.nodeConfiguration == nil {
		unexpectedCall(f.t, "NodeConfiguration")
	}
	return f.nodeConfiguration(a0, a1, a2, a3)
}

func (f *fakeDeployment) NodeStatus(a0 context.Context, a1 string, a2 string) (deployment.NodeStatus, error) {
	if f.nodeStatus == nil {
		unexpectedCall(f.t, "NodeStatus")
	}
	return f.nodeStatus(a0, a1, a2)
}

func (f *fakeDeployment) DecodeConfiguration(a0 string, a1 json.RawMessage, a2 json.RawMessage) (sandbox.Configuration, error) {
	if f.decodeConfiguration == nil {
		unexpectedCall(f.t, "DecodeConfiguration")
	}
	return f.decodeConfiguration(a0, a1, a2)
}

type fakeNodeAllocations struct {
	t                          testing.TB
	listNodeRuntimeAllocations func(context.Context, string) ([]store.RuntimeNodeAllocation, error)
}

func (f *fakeNodeAllocations) ListNodeRuntimeAllocations(a0 context.Context, a1 string) ([]store.RuntimeNodeAllocation, error) {
	if f.listNodeRuntimeAllocations == nil {
		unexpectedCall(f.t, "ListNodeRuntimeAllocations")
	}
	return f.listNodeRuntimeAllocations(a0, a1)
}

type fakeDeploymentChanges struct {
	t                           testing.TB
	initializeSandboxDeployment func(context.Context, sandbox.Selection) (deployment.View, error)
	updateSandboxDeployment     func(context.Context, sandbox.Selection) (deployment.View, error)
}

func (f *fakeDeploymentChanges) InitializeSandboxDeployment(a0 context.Context, a1 sandbox.Selection) (deployment.View, error) {
	if f.initializeSandboxDeployment == nil {
		unexpectedCall(f.t, "InitializeSandboxDeployment")
	}
	return f.initializeSandboxDeployment(a0, a1)
}

func (f *fakeDeploymentChanges) UpdateSandboxDeployment(a0 context.Context, a1 sandbox.Selection) (deployment.View, error) {
	if f.updateSandboxDeployment == nil {
		unexpectedCall(f.t, "UpdateSandboxDeployment")
	}
	return f.updateSandboxDeployment(a0, a1)
}

type fakeDeploymentReset struct {
	t                  testing.TB
	startSandboxReset  func(context.Context, store.SandboxResetRequest) (deployment.View, error)
	cancelSandboxReset func(context.Context, uint64) (deployment.View, error)
}

func (f *fakeDeploymentReset) StartSandboxReset(a0 context.Context, a1 store.SandboxResetRequest) (deployment.View, error) {
	if f.startSandboxReset == nil {
		unexpectedCall(f.t, "StartSandboxReset")
	}
	return f.startSandboxReset(a0, a1)
}

func (f *fakeDeploymentReset) CancelSandboxReset(a0 context.Context, a1 uint64) (deployment.View, error) {
	if f.cancelSandboxReset == nil {
		unexpectedCall(f.t, "CancelSandboxReset")
	}
	return f.cancelSandboxReset(a0, a1)
}

type fakeEnvironmentTemplates struct {
	t      testing.TB
	create func(context.Context, environmenttemplates.CreateCommand) (environmenttemplates.Template, error)
	update func(context.Context, environmenttemplates.UpdateCommand) (environmenttemplates.Template, error)
	delete func(context.Context, environmenttemplates.DeleteCommand) (string, error)
}

func (f *fakeEnvironmentTemplates) Create(a0 context.Context, a1 environmenttemplates.CreateCommand) (environmenttemplates.Template, error) {
	if f.create == nil {
		unexpectedCall(f.t, "Create")
	}
	return f.create(a0, a1)
}

func (f *fakeEnvironmentTemplates) Update(a0 context.Context, a1 environmenttemplates.UpdateCommand) (environmenttemplates.Template, error) {
	if f.update == nil {
		unexpectedCall(f.t, "Update")
	}
	return f.update(a0, a1)
}

func (f *fakeEnvironmentTemplates) Delete(a0 context.Context, a1 environmenttemplates.DeleteCommand) (string, error) {
	if f.delete == nil {
		unexpectedCall(f.t, "Delete")
	}
	return f.delete(a0, a1)
}

type fakeEnvironmentTemplatesReader struct {
	t       testing.TB
	get     func(context.Context, string, string) (environmenttemplates.Template, error)
	list    func(context.Context, string, environmenttemplates.ListQuery) (environmenttemplates.Page, error)
	resolve func(context.Context, string, string) (environmenttemplates.Resolved, error)
}

func (f *fakeEnvironmentTemplatesReader) Get(a0 context.Context, a1 string, a2 string) (environmenttemplates.Template, error) {
	if f.get == nil {
		unexpectedCall(f.t, "Get")
	}
	return f.get(a0, a1, a2)
}

func (f *fakeEnvironmentTemplatesReader) List(a0 context.Context, a1 string, a2 environmenttemplates.ListQuery) (environmenttemplates.Page, error) {
	if f.list == nil {
		unexpectedCall(f.t, "List")
	}
	return f.list(a0, a1, a2)
}

func (f *fakeEnvironmentTemplatesReader) Resolve(a0 context.Context, a1 string, a2 string) (environmenttemplates.Resolved, error) {
	if f.resolve == nil {
		unexpectedCall(f.t, "Resolve")
	}
	return f.resolve(a0, a1, a2)
}

type fakeEnvironmentWorkspaces struct {
	t                        testing.TB
	readEnvironmentDirectory func(context.Context, sessions.Environment, string) (proto.WorkspaceDirectoryResult, error)
	writeEnvironmentFile     func(context.Context, sessions.Environment, string, []byte) (int64, error)
}

func (f *fakeEnvironmentWorkspaces) ReadEnvironmentDirectory(a0 context.Context, a1 sessions.Environment, a2 string) (proto.WorkspaceDirectoryResult, error) {
	if f.readEnvironmentDirectory == nil {
		unexpectedCall(f.t, "ReadEnvironmentDirectory")
	}
	return f.readEnvironmentDirectory(a0, a1, a2)
}

func (f *fakeEnvironmentWorkspaces) WriteEnvironmentFile(a0 context.Context, a1 sessions.Environment, a2 string, a3 []byte) (int64, error) {
	if f.writeEnvironmentFile == nil {
		unexpectedCall(f.t, "WriteEnvironmentFile")
	}
	return f.writeEnvironmentFile(a0, a1, a2, a3)
}

type fakeEnvironments struct {
	t                                testing.TB
	getEnvironment                   func(context.Context, string, string) (sessions.Environment, error)
	authorizeEnvironmentInstallation func(context.Context, identity.Principal, string, string) (string, int64, error)
	validateEnvironmentInstallation  func(context.Context, string, string) (sessions.InstallationAuthorization, error)
	claimEnvironmentInstallation     func(context.Context, string, string, string) error
	projectExecutorCredentialState   func(context.Context, identity.Principal, string) (sessions.ExecutorCredentialState, error)
	issueProjectExecutorCredential   func(context.Context, identity.Principal, string, string, bool) (sessions.IssuedExecutorCredential, error)
	revokeProjectExecutorCredential  func(context.Context, identity.Principal, string, string) error
}

func (f *fakeEnvironments) GetEnvironment(a0 context.Context, a1 string, a2 string) (sessions.Environment, error) {
	if f.getEnvironment == nil {
		unexpectedCall(f.t, "GetEnvironment")
	}
	return f.getEnvironment(a0, a1, a2)
}

func (f *fakeEnvironments) AuthorizeEnvironmentInstallation(a0 context.Context, a1 identity.Principal, a2 string, a3 string) (string, int64, error) {
	if f.authorizeEnvironmentInstallation == nil {
		unexpectedCall(f.t, "AuthorizeEnvironmentInstallation")
	}
	return f.authorizeEnvironmentInstallation(a0, a1, a2, a3)
}

func (f *fakeEnvironments) ValidateEnvironmentInstallation(a0 context.Context, a1 string, a2 string) (sessions.InstallationAuthorization, error) {
	if f.validateEnvironmentInstallation == nil {
		unexpectedCall(f.t, "ValidateEnvironmentInstallation")
	}
	return f.validateEnvironmentInstallation(a0, a1, a2)
}

func (f *fakeEnvironments) ClaimEnvironmentInstallation(a0 context.Context, a1 string, a2 string, a3 string) error {
	if f.claimEnvironmentInstallation == nil {
		unexpectedCall(f.t, "ClaimEnvironmentInstallation")
	}
	return f.claimEnvironmentInstallation(a0, a1, a2, a3)
}

func (f *fakeEnvironments) ProjectExecutorCredentialState(a0 context.Context, a1 identity.Principal, a2 string) (sessions.ExecutorCredentialState, error) {
	if f.projectExecutorCredentialState == nil {
		unexpectedCall(f.t, "ProjectExecutorCredentialState")
	}
	return f.projectExecutorCredentialState(a0, a1, a2)
}

func (f *fakeEnvironments) IssueProjectExecutorCredential(a0 context.Context, a1 identity.Principal, a2 string, a3 string, a4 bool) (sessions.IssuedExecutorCredential, error) {
	if f.issueProjectExecutorCredential == nil {
		unexpectedCall(f.t, "IssueProjectExecutorCredential")
	}
	return f.issueProjectExecutorCredential(a0, a1, a2, a3, a4)
}

func (f *fakeEnvironments) RevokeProjectExecutorCredential(a0 context.Context, a1 identity.Principal, a2 string, a3 string) error {
	if f.revokeProjectExecutorCredential == nil {
		unexpectedCall(f.t, "RevokeProjectExecutorCredential")
	}
	return f.revokeProjectExecutorCredential(a0, a1, a2, a3)
}

type fakeExecutorConnections struct {
	t                 testing.TB
	executorConnected func(ctx context.Context, environmentID, credentialDigest string) (bool, error)
}

func (f *fakeExecutorConnections) ExecutorConnected(a0 context.Context, a1 string, a2 string) (bool, error) {
	if f.executorConnected == nil {
		unexpectedCall(f.t, "ExecutorConnected")
	}
	return f.executorConnected(a0, a1, a2)
}

type fakeFilesReader struct {
	t    testing.TB
	get  func(ctx context.Context, tenantID, fileID string) (files.File, error)
	list func(ctx context.Context, tenantID string, query files.ListQuery) (files.Page, error)
	read func(ctx context.Context, tenantID, fileID string, consume func(files.File, io.Reader) error) error
}

func (f *fakeFilesReader) Get(a0 context.Context, a1 string, a2 string) (files.File, error) {
	if f.get == nil {
		unexpectedCall(f.t, "Get")
	}
	return f.get(a0, a1, a2)
}

func (f *fakeFilesReader) List(a0 context.Context, a1 string, a2 files.ListQuery) (files.Page, error) {
	if f.list == nil {
		unexpectedCall(f.t, "List")
	}
	return f.list(a0, a1, a2)
}

func (f *fakeFilesReader) Read(a0 context.Context, a1 string, a2 string, a3 func(files.File, io.Reader) error) error {
	if f.read == nil {
		unexpectedCall(f.t, "Read")
	}
	return f.read(a0, a1, a2, a3)
}

type fakeFiles struct {
	t      testing.TB
	create func(context.Context, files.CreateCommand) (files.File, error)
	delete func(context.Context, files.DeleteCommand) error
}

func (f *fakeFiles) Create(a0 context.Context, a1 files.CreateCommand) (files.File, error) {
	if f.create == nil {
		unexpectedCall(f.t, "Create")
	}
	return f.create(a0, a1)
}

func (f *fakeFiles) Delete(a0 context.Context, a1 files.DeleteCommand) error {
	if f.delete == nil {
		unexpectedCall(f.t, "Delete")
	}
	return f.delete(a0, a1)
}

type fakeInputAdmission struct {
	t            testing.TB
	submitInputs func(context.Context, string, string, string, []sessions.Input) ([]sessions.InputReceipt, error)
}

func (f *fakeInputAdmission) SubmitInputs(a0 context.Context, a1 string, a2 string, a3 string, a4 []sessions.Input) ([]sessions.InputReceipt, error) {
	if f.submitInputs == nil {
		unexpectedCall(f.t, "SubmitInputs")
	}
	return f.submitInputs(a0, a1, a2, a3, a4)
}

type fakeInstallationBindings struct {
	t               testing.TB
	addressBindings func(context.Context) (store.AddressBindings, error)
}

func (f *fakeInstallationBindings) AddressBindings(a0 context.Context) (store.AddressBindings, error) {
	if f.addressBindings == nil {
		unexpectedCall(f.t, "AddressBindings")
	}
	return f.addressBindings(a0)
}

type fakeItems struct {
	t         testing.TB
	listItems func(context.Context, string, string, string, int, bool) (sessions.ItemPage, error)
}

func (f *fakeItems) ListItems(a0 context.Context, a1 string, a2 string, a3 string, a4 int, a5 bool) (sessions.ItemPage, error) {
	if f.listItems == nil {
		unexpectedCall(f.t, "ListItems")
	}
	return f.listItems(a0, a1, a2, a3, a4, a5)
}

type fakeMetrics struct {
	t                 testing.TB
	read              func(context.Context, string) (coremetrics.View, error)
	recordUnavailable func()
}

func (f *fakeMetrics) Read(a0 context.Context, a1 string) (coremetrics.View, error) {
	if f.read == nil {
		unexpectedCall(f.t, "Read")
	}
	return f.read(a0, a1)
}

func (f *fakeMetrics) RecordUnavailable() {
	if f.recordUnavailable == nil {
		unexpectedCall(f.t, "RecordUnavailable")
	}
	f.recordUnavailable()
}

type fakeModelProviders struct {
	t       testing.TB
	replace func(context.Context, modelconfiguration.Replacement) (modelconfiguration.Configuration, error)
	delete  func(context.Context, string) error
	resolve func(context.Context, string) (*modelconfiguration.Snapshot, error)
}

func (f *fakeModelProviders) Replace(a0 context.Context, a1 modelconfiguration.Replacement) (modelconfiguration.Configuration, error) {
	if f.replace == nil {
		unexpectedCall(f.t, "Replace")
	}
	return f.replace(a0, a1)
}

func (f *fakeModelProviders) Delete(a0 context.Context, a1 string) error {
	if f.delete == nil {
		unexpectedCall(f.t, "Delete")
	}
	return f.delete(a0, a1)
}

func (f *fakeModelProviders) Resolve(a0 context.Context, a1 string) (*modelconfiguration.Snapshot, error) {
	if f.resolve == nil {
		unexpectedCall(f.t, "Resolve")
	}
	return f.resolve(a0, a1)
}

type fakeModelProvidersReader struct {
	t    testing.TB
	list func(context.Context) ([]modelconfiguration.Configuration, error)
}

func (f *fakeModelProvidersReader) List(a0 context.Context) ([]modelconfiguration.Configuration, error) {
	if f.list == nil {
		unexpectedCall(f.t, "List")
	}
	return f.list(a0)
}

type fakeProjects struct {
	t              testing.TB
	createProject  func(context.Context, projects.CreateProject) (projects.Project, error)
	renameProject  func(context.Context, projects.RenameProject) (projects.Project, error)
	archiveProject func(context.Context, projects.ArchiveProject) (projects.Project, error)
	createAPIKey   func(context.Context, projects.CreateAPIKey) (projects.IssuedAPIKey, error)
	revokeAPIKey   func(context.Context, projects.RevokeAPIKey) error
}

func (f *fakeProjects) CreateProject(a0 context.Context, a1 projects.CreateProject) (projects.Project, error) {
	if f.createProject == nil {
		unexpectedCall(f.t, "CreateProject")
	}
	return f.createProject(a0, a1)
}

func (f *fakeProjects) RenameProject(a0 context.Context, a1 projects.RenameProject) (projects.Project, error) {
	if f.renameProject == nil {
		unexpectedCall(f.t, "RenameProject")
	}
	return f.renameProject(a0, a1)
}

func (f *fakeProjects) ArchiveProject(a0 context.Context, a1 projects.ArchiveProject) (projects.Project, error) {
	if f.archiveProject == nil {
		unexpectedCall(f.t, "ArchiveProject")
	}
	return f.archiveProject(a0, a1)
}

func (f *fakeProjects) CreateAPIKey(a0 context.Context, a1 projects.CreateAPIKey) (projects.IssuedAPIKey, error) {
	if f.createAPIKey == nil {
		unexpectedCall(f.t, "CreateAPIKey")
	}
	return f.createAPIKey(a0, a1)
}

func (f *fakeProjects) RevokeAPIKey(a0 context.Context, a1 projects.RevokeAPIKey) error {
	if f.revokeAPIKey == nil {
		unexpectedCall(f.t, "RevokeAPIKey")
	}
	return f.revokeAPIKey(a0, a1)
}

type fakeProjectsReader struct {
	t             testing.TB
	getProject    func(context.Context, string) (projects.Binding, error)
	listProjects  func(context.Context, projects.ListQuery) (projects.Page, error)
	listAPIKeys   func(context.Context, string, projects.ListQuery) (projects.KeyPage, error)
	resolveAPIKey func(context.Context, [sha256.Size]byte) (projects.KeyBinding, error)
}

func (f *fakeProjectsReader) GetProject(a0 context.Context, a1 string) (projects.Binding, error) {
	if f.getProject == nil {
		unexpectedCall(f.t, "GetProject")
	}
	return f.getProject(a0, a1)
}

func (f *fakeProjectsReader) ListProjects(a0 context.Context, a1 projects.ListQuery) (projects.Page, error) {
	if f.listProjects == nil {
		unexpectedCall(f.t, "ListProjects")
	}
	return f.listProjects(a0, a1)
}

func (f *fakeProjectsReader) ListAPIKeys(a0 context.Context, a1 string, a2 projects.ListQuery) (projects.KeyPage, error) {
	if f.listAPIKeys == nil {
		unexpectedCall(f.t, "ListAPIKeys")
	}
	return f.listAPIKeys(a0, a1, a2)
}

func (f *fakeProjectsReader) ResolveAPIKey(a0 context.Context, a1 [sha256.Size]byte) (projects.KeyBinding, error) {
	if f.resolveAPIKey == nil {
		unexpectedCall(f.t, "ResolveAPIKey")
	}
	return f.resolveAPIKey(a0, a1)
}

type fakeRuntimeHistory struct {
	t            testing.TB
	capabilities func() runtimehistory.Capabilities
	querySession func(context.Context, string, string, runtimehistory.Range) (runtimehistory.Response, error)
}

func (f *fakeRuntimeHistory) Capabilities() runtimehistory.Capabilities {
	if f.capabilities == nil {
		unexpectedCall(f.t, "Capabilities")
	}
	return f.capabilities()
}

func (f *fakeRuntimeHistory) QuerySession(a0 context.Context, a1 string, a2 string, a3 runtimehistory.Range) (runtimehistory.Response, error) {
	if f.querySession == nil {
		unexpectedCall(f.t, "QuerySession")
	}
	return f.querySession(a0, a1, a2, a3)
}

type fakeRuntimeObservations struct {
	t               testing.TB
	observeSession  func(context.Context, string, string) (runtimeobs.Observation, error)
	observeSessions func(context.Context, []runtimeobs.SessionIdentity, runtimeobs.PageOptions) ([]runtimeobs.Observation, []error)
}

func (f *fakeRuntimeObservations) ObserveSession(a0 context.Context, a1 string, a2 string) (runtimeobs.Observation, error) {
	if f.observeSession == nil {
		unexpectedCall(f.t, "ObserveSession")
	}
	return f.observeSession(a0, a1, a2)
}

func (f *fakeRuntimeObservations) ObserveSessions(a0 context.Context, a1 []runtimeobs.SessionIdentity, a2 runtimeobs.PageOptions) ([]runtimeobs.Observation, []error) {
	if f.observeSessions == nil {
		unexpectedCall(f.t, "ObserveSessions")
	}
	return f.observeSessions(a0, a1, a2)
}

type fakeSessionAdmin struct {
	t                                testing.TB
	getSessionDiagnosticsSnapshot    func(context.Context, string, string) (sessions.Session, error)
	getTurnDiagnosticsSnapshot       func(context.Context, string, string, string) (sessions.TurnDiagnosticsSnapshot, error)
	getSessionExecutionConfiguration func(context.Context, string, string) (v1.SessionExecutionConfiguration, error)
	getManagedSessionArchive         func(context.Context, string, string) (sessions.ManagedArchive, error)
}

func (f *fakeSessionAdmin) GetSessionDiagnosticsSnapshot(a0 context.Context, a1 string, a2 string) (sessions.Session, error) {
	if f.getSessionDiagnosticsSnapshot == nil {
		unexpectedCall(f.t, "GetSessionDiagnosticsSnapshot")
	}
	return f.getSessionDiagnosticsSnapshot(a0, a1, a2)
}

func (f *fakeSessionAdmin) GetTurnDiagnosticsSnapshot(a0 context.Context, a1 string, a2 string, a3 string) (sessions.TurnDiagnosticsSnapshot, error) {
	if f.getTurnDiagnosticsSnapshot == nil {
		unexpectedCall(f.t, "GetTurnDiagnosticsSnapshot")
	}
	return f.getTurnDiagnosticsSnapshot(a0, a1, a2, a3)
}

func (f *fakeSessionAdmin) GetSessionExecutionConfiguration(a0 context.Context, a1 string, a2 string) (v1.SessionExecutionConfiguration, error) {
	if f.getSessionExecutionConfiguration == nil {
		unexpectedCall(f.t, "GetSessionExecutionConfiguration")
	}
	return f.getSessionExecutionConfiguration(a0, a1, a2)
}

func (f *fakeSessionAdmin) GetManagedSessionArchive(a0 context.Context, a1 string, a2 string) (sessions.ManagedArchive, error) {
	if f.getManagedSessionArchive == nil {
		unexpectedCall(f.t, "GetManagedSessionArchive")
	}
	return f.getManagedSessionArchive(a0, a1, a2)
}

type fakeSessionAdmission struct {
	t                   testing.TB
	createSession       func(context.Context, string, sessions.CreateSession) (sessions.Session, error)
	createSessionStream func(context.Context, string, sessions.CreateSession) (sessions.Creation, error)
}

func (f *fakeSessionAdmission) CreateSession(a0 context.Context, a1 string, a2 sessions.CreateSession) (sessions.Session, error) {
	if f.createSession == nil {
		unexpectedCall(f.t, "CreateSession")
	}
	return f.createSession(a0, a1, a2)
}

func (f *fakeSessionAdmission) CreateSessionStream(a0 context.Context, a1 string, a2 sessions.CreateSession) (sessions.Creation, error) {
	if f.createSessionStream == nil {
		unexpectedCall(f.t, "CreateSessionStream")
	}
	return f.createSessionStream(a0, a1, a2)
}

type fakeSessionArchive struct {
	t                     testing.TB
	archiveManagedSession func(context.Context, string, string, uint64) (sessions.ManagedArchive, error)
}

func (f *fakeSessionArchive) ArchiveManagedSession(a0 context.Context, a1 string, a2 string, a3 uint64) (sessions.ManagedArchive, error) {
	if f.archiveManagedSession == nil {
		unexpectedCall(f.t, "ArchiveManagedSession")
	}
	return f.archiveManagedSession(a0, a1, a2, a3)
}

type fakeSessionCreation struct {
	t                   testing.TB
	createSession       func(context.Context, string, sessions.CreateSession) (sessions.Session, error)
	createSessionStream func(context.Context, string, sessions.CreateSession) (sessions.Creation, error)
	findSessionCreation func(context.Context, string, string, json.RawMessage, identity.Subject) (sessions.Creation, error)
}

func (f *fakeSessionCreation) CreateSession(a0 context.Context, a1 string, a2 sessions.CreateSession) (sessions.Session, error) {
	if f.createSession == nil {
		unexpectedCall(f.t, "CreateSession")
	}
	return f.createSession(a0, a1, a2)
}

func (f *fakeSessionCreation) CreateSessionStream(a0 context.Context, a1 string, a2 sessions.CreateSession) (sessions.Creation, error) {
	if f.createSessionStream == nil {
		unexpectedCall(f.t, "CreateSessionStream")
	}
	return f.createSessionStream(a0, a1, a2)
}

func (f *fakeSessionCreation) FindSessionCreation(a0 context.Context, a1 string, a2 string, a3 json.RawMessage, a4 identity.Subject) (sessions.Creation, error) {
	if f.findSessionCreation == nil {
		unexpectedCall(f.t, "FindSessionCreation")
	}
	return f.findSessionCreation(a0, a1, a2, a3, a4)
}

type fakeSessionEvents struct {
	t                     testing.TB
	sessionEventCursor    func(context.Context, string, string) (int64, error)
	listSessionEvents     func(context.Context, string, string, int64) ([]sessions.SessionChange, error)
	sessionStreamSnapshot func(context.Context, string, string) (sessions.Session, int64, error)
}

func (f *fakeSessionEvents) SessionEventCursor(a0 context.Context, a1 string, a2 string) (int64, error) {
	if f.sessionEventCursor == nil {
		unexpectedCall(f.t, "SessionEventCursor")
	}
	return f.sessionEventCursor(a0, a1, a2)
}

func (f *fakeSessionEvents) ListSessionEvents(a0 context.Context, a1 string, a2 string, a3 int64) ([]sessions.SessionChange, error) {
	if f.listSessionEvents == nil {
		unexpectedCall(f.t, "ListSessionEvents")
	}
	return f.listSessionEvents(a0, a1, a2, a3)
}

func (f *fakeSessionEvents) SessionStreamSnapshot(a0 context.Context, a1 string, a2 string) (sessions.Session, int64, error) {
	if f.sessionStreamSnapshot == nil {
		unexpectedCall(f.t, "SessionStreamSnapshot")
	}
	return f.sessionStreamSnapshot(a0, a1, a2)
}

type fakeSessions struct {
	t                     testing.TB
	getSession            func(context.Context, string, string) (sessions.Session, error)
	listSessions          func(context.Context, string, string, int, bool, *string) (sessions.Page, error)
	updateSessionMetadata func(context.Context, string, string, map[string]string) (sessions.Session, error)
	deleteSession         func(context.Context, string, string) error
	auditSessionOperation func(context.Context, string, string, string) error
}

func (f *fakeSessions) GetSession(a0 context.Context, a1 string, a2 string) (sessions.Session, error) {
	if f.getSession == nil {
		unexpectedCall(f.t, "GetSession")
	}
	return f.getSession(a0, a1, a2)
}

func (f *fakeSessions) ListSessions(a0 context.Context, a1 string, a2 string, a3 int, a4 bool, a5 *string) (sessions.Page, error) {
	if f.listSessions == nil {
		unexpectedCall(f.t, "ListSessions")
	}
	return f.listSessions(a0, a1, a2, a3, a4, a5)
}

func (f *fakeSessions) UpdateSessionMetadata(a0 context.Context, a1 string, a2 string, a3 map[string]string) (sessions.Session, error) {
	if f.updateSessionMetadata == nil {
		unexpectedCall(f.t, "UpdateSessionMetadata")
	}
	return f.updateSessionMetadata(a0, a1, a2, a3)
}

func (f *fakeSessions) DeleteSession(a0 context.Context, a1 string, a2 string) error {
	if f.deleteSession == nil {
		unexpectedCall(f.t, "DeleteSession")
	}
	return f.deleteSession(a0, a1, a2)
}

func (f *fakeSessions) AuditSessionOperation(a0 context.Context, a1 string, a2 string, a3 string) error {
	if f.auditSessionOperation == nil {
		unexpectedCall(f.t, "AuditSessionOperation")
	}
	return f.auditSessionOperation(a0, a1, a2, a3)
}

type fakeSkills struct {
	t                  testing.TB
	createSkill        func(context.Context, skills.CreateSkill) (skills.Skill, error)
	createVersion      func(context.Context, skills.CreateVersion) (skills.Version, error)
	setDefaultVersion  func(context.Context, skills.SetDefaultVersion) (skills.Skill, error)
	deleteSkill        func(context.Context, skills.DeleteSkill) error
	deleteVersion      func(context.Context, skills.DeleteVersion) (skills.Version, error)
	listSkills         func(context.Context, skills.ListSkills) (skills.Page, error)
	listVersions       func(context.Context, skills.ListVersions) (skills.VersionPage, error)
	readVersion        func(context.Context, skills.ReadVersion) (skills.Content, error)
	readDefaultVersion func(context.Context, skills.ReadDefaultVersion) (skills.Content, error)
}

func (f *fakeSkills) CreateSkill(a0 context.Context, a1 skills.CreateSkill) (skills.Skill, error) {
	if f.createSkill == nil {
		unexpectedCall(f.t, "CreateSkill")
	}
	return f.createSkill(a0, a1)
}

func (f *fakeSkills) CreateVersion(a0 context.Context, a1 skills.CreateVersion) (skills.Version, error) {
	if f.createVersion == nil {
		unexpectedCall(f.t, "CreateVersion")
	}
	return f.createVersion(a0, a1)
}

func (f *fakeSkills) SetDefaultVersion(a0 context.Context, a1 skills.SetDefaultVersion) (skills.Skill, error) {
	if f.setDefaultVersion == nil {
		unexpectedCall(f.t, "SetDefaultVersion")
	}
	return f.setDefaultVersion(a0, a1)
}

func (f *fakeSkills) DeleteSkill(a0 context.Context, a1 skills.DeleteSkill) error {
	if f.deleteSkill == nil {
		unexpectedCall(f.t, "DeleteSkill")
	}
	return f.deleteSkill(a0, a1)
}

func (f *fakeSkills) DeleteVersion(a0 context.Context, a1 skills.DeleteVersion) (skills.Version, error) {
	if f.deleteVersion == nil {
		unexpectedCall(f.t, "DeleteVersion")
	}
	return f.deleteVersion(a0, a1)
}

func (f *fakeSkills) ListSkills(a0 context.Context, a1 skills.ListSkills) (skills.Page, error) {
	if f.listSkills == nil {
		unexpectedCall(f.t, "ListSkills")
	}
	return f.listSkills(a0, a1)
}

func (f *fakeSkills) ListVersions(a0 context.Context, a1 skills.ListVersions) (skills.VersionPage, error) {
	if f.listVersions == nil {
		unexpectedCall(f.t, "ListVersions")
	}
	return f.listVersions(a0, a1)
}

func (f *fakeSkills) ReadVersion(a0 context.Context, a1 skills.ReadVersion) (skills.Content, error) {
	if f.readVersion == nil {
		unexpectedCall(f.t, "ReadVersion")
	}
	return f.readVersion(a0, a1)
}

func (f *fakeSkills) ReadDefaultVersion(a0 context.Context, a1 skills.ReadDefaultVersion) (skills.Content, error) {
	if f.readDefaultVersion == nil {
		unexpectedCall(f.t, "ReadDefaultVersion")
	}
	return f.readDefaultVersion(a0, a1)
}

type fakeSkillsReader struct {
	t       testing.TB
	skill   func(context.Context, string, uuid.UUID) (skills.Skill, error)
	version func(context.Context, string, uuid.UUID, int64) (skills.Version, error)
}

func (f *fakeSkillsReader) Skill(a0 context.Context, a1 string, a2 uuid.UUID) (skills.Skill, error) {
	if f.skill == nil {
		unexpectedCall(f.t, "Skill")
	}
	return f.skill(a0, a1, a2)
}

func (f *fakeSkillsReader) Version(a0 context.Context, a1 string, a2 uuid.UUID, a3 int64) (skills.Version, error) {
	if f.version == nil {
		unexpectedCall(f.t, "Version")
	}
	return f.version(a0, a1, a2, a3)
}

type fakeSubagents struct {
	t                     testing.TB
	getSubagent           func(context.Context, string, string, string) (v1.Subagent, error)
	listSubagents         func(context.Context, string, string, string, int, bool) (v1.SubagentList, error)
	listSubagentItems     func(context.Context, string, string, string, string, int, bool) (v1.ItemList, error)
	getSubagentTurn       func(context.Context, string, string, string, string) (v1.Turn, error)
	listSubagentTurns     func(context.Context, string, string, string, string, int, bool) (v1.TurnList, error)
	listSubagentTurnItems func(context.Context, string, string, string, string, string, int, bool) (v1.ItemList, error)
}

func (f *fakeSubagents) GetSubagent(a0 context.Context, a1 string, a2 string, a3 string) (v1.Subagent, error) {
	if f.getSubagent == nil {
		unexpectedCall(f.t, "GetSubagent")
	}
	return f.getSubagent(a0, a1, a2, a3)
}

func (f *fakeSubagents) ListSubagents(a0 context.Context, a1 string, a2 string, a3 string, a4 int, a5 bool) (v1.SubagentList, error) {
	if f.listSubagents == nil {
		unexpectedCall(f.t, "ListSubagents")
	}
	return f.listSubagents(a0, a1, a2, a3, a4, a5)
}

func (f *fakeSubagents) ListSubagentItems(a0 context.Context, a1 string, a2 string, a3 string, a4 string, a5 int, a6 bool) (v1.ItemList, error) {
	if f.listSubagentItems == nil {
		unexpectedCall(f.t, "ListSubagentItems")
	}
	return f.listSubagentItems(a0, a1, a2, a3, a4, a5, a6)
}

func (f *fakeSubagents) GetSubagentTurn(a0 context.Context, a1 string, a2 string, a3 string, a4 string) (v1.Turn, error) {
	if f.getSubagentTurn == nil {
		unexpectedCall(f.t, "GetSubagentTurn")
	}
	return f.getSubagentTurn(a0, a1, a2, a3, a4)
}

func (f *fakeSubagents) ListSubagentTurns(a0 context.Context, a1 string, a2 string, a3 string, a4 string, a5 int, a6 bool) (v1.TurnList, error) {
	if f.listSubagentTurns == nil {
		unexpectedCall(f.t, "ListSubagentTurns")
	}
	return f.listSubagentTurns(a0, a1, a2, a3, a4, a5, a6)
}

func (f *fakeSubagents) ListSubagentTurnItems(a0 context.Context, a1 string, a2 string, a3 string, a4 string, a5 string, a6 int, a7 bool) (v1.ItemList, error) {
	if f.listSubagentTurnItems == nil {
		unexpectedCall(f.t, "ListSubagentTurnItems")
	}
	return f.listSubagentTurnItems(a0, a1, a2, a3, a4, a5, a6, a7)
}

type fakeTurns struct {
	t         testing.TB
	getTurn   func(context.Context, string, string, string) (sessions.Turn, error)
	listTurns func(context.Context, string, string, string, int, bool) (sessions.TurnPage, error)
}

func (f *fakeTurns) GetTurn(a0 context.Context, a1 string, a2 string, a3 string) (sessions.Turn, error) {
	if f.getTurn == nil {
		unexpectedCall(f.t, "GetTurn")
	}
	return f.getTurn(a0, a1, a2, a3)
}

func (f *fakeTurns) ListTurns(a0 context.Context, a1 string, a2 string, a3 string, a4 int, a5 bool) (sessions.TurnPage, error) {
	if f.listTurns == nil {
		unexpectedCall(f.t, "ListTurns")
	}
	return f.listTurns(a0, a1, a2, a3, a4, a5)
}

type fakeVaults struct {
	t                      testing.TB
	createVault            func(context.Context, vaults.CreateVault) (vaults.Vault, error)
	deleteVault            func(context.Context, vaults.DeleteVault) (string, error)
	createStaticCredential func(context.Context, vaults.CreateStaticCredential) (vaults.Credential, error)
	updateStaticCredential func(context.Context, vaults.UpdateStaticCredential) (vaults.Credential, error)
	createOAuthCredential  func(context.Context, vaults.CreateOAuthCredential) (vaults.Credential, error)
	updateOAuthCredential  func(context.Context, vaults.UpdateOAuthCredential) (vaults.Credential, error)
	deleteCredential       func(context.Context, vaults.DeleteCredential) (string, error)
	resolveMCPCredentials  func(context.Context, vaults.ResolveMCPCredentials) ([]vaults.MCPCredentialBinding, error)
}

func (f *fakeVaults) CreateVault(a0 context.Context, a1 vaults.CreateVault) (vaults.Vault, error) {
	if f.createVault == nil {
		unexpectedCall(f.t, "CreateVault")
	}
	return f.createVault(a0, a1)
}

func (f *fakeVaults) DeleteVault(a0 context.Context, a1 vaults.DeleteVault) (string, error) {
	if f.deleteVault == nil {
		unexpectedCall(f.t, "DeleteVault")
	}
	return f.deleteVault(a0, a1)
}

func (f *fakeVaults) CreateStaticCredential(a0 context.Context, a1 vaults.CreateStaticCredential) (vaults.Credential, error) {
	if f.createStaticCredential == nil {
		unexpectedCall(f.t, "CreateStaticCredential")
	}
	return f.createStaticCredential(a0, a1)
}

func (f *fakeVaults) UpdateStaticCredential(a0 context.Context, a1 vaults.UpdateStaticCredential) (vaults.Credential, error) {
	if f.updateStaticCredential == nil {
		unexpectedCall(f.t, "UpdateStaticCredential")
	}
	return f.updateStaticCredential(a0, a1)
}

func (f *fakeVaults) CreateOAuthCredential(a0 context.Context, a1 vaults.CreateOAuthCredential) (vaults.Credential, error) {
	if f.createOAuthCredential == nil {
		unexpectedCall(f.t, "CreateOAuthCredential")
	}
	return f.createOAuthCredential(a0, a1)
}

func (f *fakeVaults) UpdateOAuthCredential(a0 context.Context, a1 vaults.UpdateOAuthCredential) (vaults.Credential, error) {
	if f.updateOAuthCredential == nil {
		unexpectedCall(f.t, "UpdateOAuthCredential")
	}
	return f.updateOAuthCredential(a0, a1)
}

func (f *fakeVaults) DeleteCredential(a0 context.Context, a1 vaults.DeleteCredential) (string, error) {
	if f.deleteCredential == nil {
		unexpectedCall(f.t, "DeleteCredential")
	}
	return f.deleteCredential(a0, a1)
}

func (f *fakeVaults) ResolveMCPCredentials(a0 context.Context, a1 vaults.ResolveMCPCredentials) ([]vaults.MCPCredentialBinding, error) {
	if f.resolveMCPCredentials == nil {
		unexpectedCall(f.t, "ResolveMCPCredentials")
	}
	return f.resolveMCPCredentials(a0, a1)
}

type fakeVaultsReader struct {
	t               testing.TB
	getVault        func(context.Context, string, string) (vaults.Vault, error)
	listVaults      func(context.Context, string, vaults.PageQuery) (vaults.VaultPage, error)
	getCredential   func(context.Context, string, string, string) (vaults.Credential, error)
	listCredentials func(context.Context, string, string, vaults.PageQuery) (vaults.CredentialPage, error)
}

func (f *fakeVaultsReader) GetVault(a0 context.Context, a1 string, a2 string) (vaults.Vault, error) {
	if f.getVault == nil {
		unexpectedCall(f.t, "GetVault")
	}
	return f.getVault(a0, a1, a2)
}

func (f *fakeVaultsReader) ListVaults(a0 context.Context, a1 string, a2 vaults.PageQuery) (vaults.VaultPage, error) {
	if f.listVaults == nil {
		unexpectedCall(f.t, "ListVaults")
	}
	return f.listVaults(a0, a1, a2)
}

func (f *fakeVaultsReader) GetCredential(a0 context.Context, a1 string, a2 string, a3 string) (vaults.Credential, error) {
	if f.getCredential == nil {
		unexpectedCall(f.t, "GetCredential")
	}
	return f.getCredential(a0, a1, a2, a3)
}

func (f *fakeVaultsReader) ListCredentials(a0 context.Context, a1 string, a2 string, a3 vaults.PageQuery) (vaults.CredentialPage, error) {
	if f.listCredentials == nil {
		unexpectedCall(f.t, "ListCredentials")
	}
	return f.listCredentials(a0, a1, a2, a3)
}

type fakeWriteAudit struct {
	t                   testing.TB
	getResourceOwners   func(context.Context, string, string, []string) ([]writeaudit.ResourceOwner, error)
	listWriteOperations func(context.Context, string, writeaudit.Filter) (writeaudit.Page, error)
}

func (f *fakeWriteAudit) GetResourceOwners(a0 context.Context, a1 string, a2 string, a3 []string) ([]writeaudit.ResourceOwner, error) {
	if f.getResourceOwners == nil {
		unexpectedCall(f.t, "GetResourceOwners")
	}
	return f.getResourceOwners(a0, a1, a2, a3)
}

func (f *fakeWriteAudit) ListWriteOperations(a0 context.Context, a1 string, a2 writeaudit.Filter) (writeaudit.Page, error) {
	if f.listWriteOperations == nil {
		unexpectedCall(f.t, "ListWriteOperations")
	}
	return f.listWriteOperations(a0, a1, a2)
}
