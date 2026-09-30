package api

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimehistory"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
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
	readAdminSummary        func(context.Context, string, store.AdminSummaryFilter, func(store.Session, *string) error) (store.AdminAssetCounts, error)
	listAdminRuntimeTargets func(context.Context, []string, string, int, bool) (store.AdminRuntimeTargetPage, error)
	listAdminAudit          func(context.Context, store.AdminAuditFilter) (store.AdminAuditPage, error)
}

func (f *fakeAdmin) ReadAdminSummary(a0 context.Context, a1 string, a2 store.AdminSummaryFilter, a3 func(store.Session, *string) error) (store.AdminAssetCounts, error) {
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

func (f *fakeAdmin) ListAdminAudit(a0 context.Context, a1 store.AdminAuditFilter) (store.AdminAuditPage, error) {
	if f.listAdminAudit == nil {
		unexpectedCall(f.t, "ListAdminAudit")
	}
	return f.listAdminAudit(a0, a1)
}

type fakeAdmission struct {
	t                   testing.TB
	createSession       func(context.Context, string, store.CreateSessionInput) (store.Session, error)
	createSessionStream func(context.Context, string, store.CreateSessionInput) (store.SessionCreation, error)
	submitInputs        func(context.Context, string, string, string, []store.Input) ([]store.InputReceipt, error)
}

func (f *fakeAdmission) CreateSession(a0 context.Context, a1 string, a2 store.CreateSessionInput) (store.Session, error) {
	if f.createSession == nil {
		unexpectedCall(f.t, "CreateSession")
	}
	return f.createSession(a0, a1, a2)
}

func (f *fakeAdmission) CreateSessionStream(a0 context.Context, a1 string, a2 store.CreateSessionInput) (store.SessionCreation, error) {
	if f.createSessionStream == nil {
		unexpectedCall(f.t, "CreateSessionStream")
	}
	return f.createSessionStream(a0, a1, a2)
}

func (f *fakeAdmission) SubmitInputs(a0 context.Context, a1 string, a2 string, a3 string, a4 []store.Input) ([]store.InputReceipt, error) {
	if f.submitInputs == nil {
		unexpectedCall(f.t, "SubmitInputs")
	}
	return f.submitInputs(a0, a1, a2, a3, a4)
}

type fakeAgents struct {
	t                  testing.TB
	deleteAgent        func(context.Context, string, string) (string, error)
	updateAgent        func(context.Context, string, string, store.UpdateAgentInput) (store.SavedAgent, error)
	listAgents         func(context.Context, string, string, int, bool) (store.AgentPage, error)
	createAgent        func(context.Context, string, store.CreateAgentInput) (store.SavedAgent, error)
	getAgent           func(context.Context, string, string) (store.SavedAgent, error)
	getAgentForSession func(context.Context, string, string, bool) (store.SavedAgent, *v1.ModelProviderInput, error)
}

func (f *fakeAgents) DeleteAgent(a0 context.Context, a1 string, a2 string) (string, error) {
	if f.deleteAgent == nil {
		unexpectedCall(f.t, "DeleteAgent")
	}
	return f.deleteAgent(a0, a1, a2)
}

func (f *fakeAgents) UpdateAgent(a0 context.Context, a1 string, a2 string, a3 store.UpdateAgentInput) (store.SavedAgent, error) {
	if f.updateAgent == nil {
		unexpectedCall(f.t, "UpdateAgent")
	}
	return f.updateAgent(a0, a1, a2, a3)
}

func (f *fakeAgents) ListAgents(a0 context.Context, a1 string, a2 string, a3 int, a4 bool) (store.AgentPage, error) {
	if f.listAgents == nil {
		unexpectedCall(f.t, "ListAgents")
	}
	return f.listAgents(a0, a1, a2, a3, a4)
}

func (f *fakeAgents) CreateAgent(a0 context.Context, a1 string, a2 store.CreateAgentInput) (store.SavedAgent, error) {
	if f.createAgent == nil {
		unexpectedCall(f.t, "CreateAgent")
	}
	return f.createAgent(a0, a1, a2)
}

func (f *fakeAgents) GetAgent(a0 context.Context, a1 string, a2 string) (store.SavedAgent, error) {
	if f.getAgent == nil {
		unexpectedCall(f.t, "GetAgent")
	}
	return f.getAgent(a0, a1, a2)
}

func (f *fakeAgents) GetAgentForSession(a0 context.Context, a1 string, a2 string, a3 bool) (store.SavedAgent, *v1.ModelProviderInput, error) {
	if f.getAgentForSession == nil {
		unexpectedCall(f.t, "GetAgentForSession")
	}
	return f.getAgentForSession(a0, a1, a2, a3)
}

type fakeArtifacts struct {
	t                     testing.TB
	getSessionArtifact    func(context.Context, string, string, string) (store.SessionArtifact, error)
	listSessionArtifacts  func(context.Context, string, string, string, string, int, bool) (store.ArtifactPage, error)
	readSessionArtifact   func(context.Context, string, string, string, func(store.SessionArtifact, io.Reader) error) error
	deleteSessionArtifact func(context.Context, string, string, string) error
}

func (f *fakeArtifacts) GetSessionArtifact(a0 context.Context, a1 string, a2 string, a3 string) (store.SessionArtifact, error) {
	if f.getSessionArtifact == nil {
		unexpectedCall(f.t, "GetSessionArtifact")
	}
	return f.getSessionArtifact(a0, a1, a2, a3)
}

func (f *fakeArtifacts) ListSessionArtifacts(a0 context.Context, a1 string, a2 string, a3 string, a4 string, a5 int, a6 bool) (store.ArtifactPage, error) {
	if f.listSessionArtifacts == nil {
		unexpectedCall(f.t, "ListSessionArtifacts")
	}
	return f.listSessionArtifacts(a0, a1, a2, a3, a4, a5, a6)
}

func (f *fakeArtifacts) ReadSessionArtifact(a0 context.Context, a1 string, a2 string, a3 string, a4 func(store.SessionArtifact, io.Reader) error) error {
	if f.readSessionArtifact == nil {
		unexpectedCall(f.t, "ReadSessionArtifact")
	}
	return f.readSessionArtifact(a0, a1, a2, a3, a4)
}

func (f *fakeArtifacts) DeleteSessionArtifact(a0 context.Context, a1 string, a2 string, a3 string) error {
	if f.deleteSessionArtifact == nil {
		unexpectedCall(f.t, "DeleteSessionArtifact")
	}
	return f.deleteSessionArtifact(a0, a1, a2, a3)
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
	t                                  testing.TB
	getRuntimeDeployment               func(context.Context) (store.RuntimeDeploymentView, error)
	listRuntimeNodes                   func(context.Context) ([]store.RuntimeNode, error)
	getRuntimeNodeDetail               func(context.Context, string, string) (store.RuntimeNodeDetail, error)
	updateRuntimeNode                  func(context.Context, string, store.RuntimeNodeUpdate) error
	removeRuntimeNode                  func(context.Context, string) error
	listNodeRuntimeAllocations         func(context.Context, string) ([]store.RuntimeNodeAllocation, error)
	createRuntimeEnrollment            func(context.Context, store.RuntimeNodeCapacity) (store.RuntimeNodeEnrollmentToken, error)
	enrollRuntimeNode                  func(context.Context, string, store.RuntimeNodeEnrollment) (store.RuntimeNodeIdentity, error)
	runtimeNodeGenerationConfiguration func(context.Context, string, string, uint64) (store.RuntimeNodeConfiguration, error)
	runtimeNodeStatus                  func(context.Context, string, string) (store.RuntimeNodeStatus, error)
}

func (f *fakeDeployment) GetRuntimeDeployment(a0 context.Context) (store.RuntimeDeploymentView, error) {
	if f.getRuntimeDeployment == nil {
		unexpectedCall(f.t, "GetRuntimeDeployment")
	}
	return f.getRuntimeDeployment(a0)
}

func (f *fakeDeployment) ListRuntimeNodes(a0 context.Context) ([]store.RuntimeNode, error) {
	if f.listRuntimeNodes == nil {
		unexpectedCall(f.t, "ListRuntimeNodes")
	}
	return f.listRuntimeNodes(a0)
}

func (f *fakeDeployment) GetRuntimeNodeDetail(a0 context.Context, a1 string, a2 string) (store.RuntimeNodeDetail, error) {
	if f.getRuntimeNodeDetail == nil {
		unexpectedCall(f.t, "GetRuntimeNodeDetail")
	}
	return f.getRuntimeNodeDetail(a0, a1, a2)
}

func (f *fakeDeployment) UpdateRuntimeNode(a0 context.Context, a1 string, a2 store.RuntimeNodeUpdate) error {
	if f.updateRuntimeNode == nil {
		unexpectedCall(f.t, "UpdateRuntimeNode")
	}
	return f.updateRuntimeNode(a0, a1, a2)
}

func (f *fakeDeployment) RemoveRuntimeNode(a0 context.Context, a1 string) error {
	if f.removeRuntimeNode == nil {
		unexpectedCall(f.t, "RemoveRuntimeNode")
	}
	return f.removeRuntimeNode(a0, a1)
}

func (f *fakeDeployment) ListNodeRuntimeAllocations(a0 context.Context, a1 string) ([]store.RuntimeNodeAllocation, error) {
	if f.listNodeRuntimeAllocations == nil {
		unexpectedCall(f.t, "ListNodeRuntimeAllocations")
	}
	return f.listNodeRuntimeAllocations(a0, a1)
}

func (f *fakeDeployment) CreateRuntimeEnrollment(a0 context.Context, a1 store.RuntimeNodeCapacity) (store.RuntimeNodeEnrollmentToken, error) {
	if f.createRuntimeEnrollment == nil {
		unexpectedCall(f.t, "CreateRuntimeEnrollment")
	}
	return f.createRuntimeEnrollment(a0, a1)
}

func (f *fakeDeployment) EnrollRuntimeNode(a0 context.Context, a1 string, a2 store.RuntimeNodeEnrollment) (store.RuntimeNodeIdentity, error) {
	if f.enrollRuntimeNode == nil {
		unexpectedCall(f.t, "EnrollRuntimeNode")
	}
	return f.enrollRuntimeNode(a0, a1, a2)
}

func (f *fakeDeployment) RuntimeNodeGenerationConfiguration(a0 context.Context, a1 string, a2 string, a3 uint64) (store.RuntimeNodeConfiguration, error) {
	if f.runtimeNodeGenerationConfiguration == nil {
		unexpectedCall(f.t, "RuntimeNodeGenerationConfiguration")
	}
	return f.runtimeNodeGenerationConfiguration(a0, a1, a2, a3)
}

func (f *fakeDeployment) RuntimeNodeStatus(a0 context.Context, a1 string, a2 string) (store.RuntimeNodeStatus, error) {
	if f.runtimeNodeStatus == nil {
		unexpectedCall(f.t, "RuntimeNodeStatus")
	}
	return f.runtimeNodeStatus(a0, a1, a2)
}

type fakeDeploymentChanges struct {
	t                           testing.TB
	initializeSandboxDeployment func(context.Context, store.SandboxDeploymentSetupRequest) (store.RuntimeDeploymentView, error)
	updateSandboxDeployment     func(context.Context, store.SandboxDeploymentUpdateRequest) (store.RuntimeDeploymentView, error)
	startSandboxReset           func(context.Context, store.SandboxResetRequest) (store.RuntimeDeploymentView, error)
	cancelSandboxReset          func(context.Context, uint64) (store.RuntimeDeploymentView, error)
}

func (f *fakeDeploymentChanges) InitializeSandboxDeployment(a0 context.Context, a1 store.SandboxDeploymentSetupRequest) (store.RuntimeDeploymentView, error) {
	if f.initializeSandboxDeployment == nil {
		unexpectedCall(f.t, "InitializeSandboxDeployment")
	}
	return f.initializeSandboxDeployment(a0, a1)
}

func (f *fakeDeploymentChanges) UpdateSandboxDeployment(a0 context.Context, a1 store.SandboxDeploymentUpdateRequest) (store.RuntimeDeploymentView, error) {
	if f.updateSandboxDeployment == nil {
		unexpectedCall(f.t, "UpdateSandboxDeployment")
	}
	return f.updateSandboxDeployment(a0, a1)
}

func (f *fakeDeploymentChanges) StartSandboxReset(a0 context.Context, a1 store.SandboxResetRequest) (store.RuntimeDeploymentView, error) {
	if f.startSandboxReset == nil {
		unexpectedCall(f.t, "StartSandboxReset")
	}
	return f.startSandboxReset(a0, a1)
}

func (f *fakeDeploymentChanges) CancelSandboxReset(a0 context.Context, a1 uint64) (store.RuntimeDeploymentView, error) {
	if f.cancelSandboxReset == nil {
		unexpectedCall(f.t, "CancelSandboxReset")
	}
	return f.cancelSandboxReset(a0, a1)
}

type fakeEnvironmentTemplates struct {
	t                          testing.TB
	resolveEnvironmentTemplate func(context.Context, string, string) (store.EnvironmentTemplate, []environmentconfig.InitialFile, error)
	createEnvironmentTemplate  func(context.Context, string, store.EnvironmentTemplateInput) (store.EnvironmentTemplate, error)
	getEnvironmentTemplate     func(context.Context, string, string) (store.EnvironmentTemplate, error)
	updateEnvironmentTemplate  func(context.Context, string, string, store.EnvironmentTemplateInput) (store.EnvironmentTemplate, error)
	deleteEnvironmentTemplate  func(context.Context, string, string) (string, error)
	listEnvironmentTemplates   func(context.Context, string, string, int, bool) (store.EnvironmentTemplatePage, error)
}

func (f *fakeEnvironmentTemplates) ResolveEnvironmentTemplate(a0 context.Context, a1 string, a2 string) (store.EnvironmentTemplate, []environmentconfig.InitialFile, error) {
	if f.resolveEnvironmentTemplate == nil {
		unexpectedCall(f.t, "ResolveEnvironmentTemplate")
	}
	return f.resolveEnvironmentTemplate(a0, a1, a2)
}

func (f *fakeEnvironmentTemplates) CreateEnvironmentTemplate(a0 context.Context, a1 string, a2 store.EnvironmentTemplateInput) (store.EnvironmentTemplate, error) {
	if f.createEnvironmentTemplate == nil {
		unexpectedCall(f.t, "CreateEnvironmentTemplate")
	}
	return f.createEnvironmentTemplate(a0, a1, a2)
}

func (f *fakeEnvironmentTemplates) GetEnvironmentTemplate(a0 context.Context, a1 string, a2 string) (store.EnvironmentTemplate, error) {
	if f.getEnvironmentTemplate == nil {
		unexpectedCall(f.t, "GetEnvironmentTemplate")
	}
	return f.getEnvironmentTemplate(a0, a1, a2)
}

func (f *fakeEnvironmentTemplates) UpdateEnvironmentTemplate(a0 context.Context, a1 string, a2 string, a3 store.EnvironmentTemplateInput) (store.EnvironmentTemplate, error) {
	if f.updateEnvironmentTemplate == nil {
		unexpectedCall(f.t, "UpdateEnvironmentTemplate")
	}
	return f.updateEnvironmentTemplate(a0, a1, a2, a3)
}

func (f *fakeEnvironmentTemplates) DeleteEnvironmentTemplate(a0 context.Context, a1 string, a2 string) (string, error) {
	if f.deleteEnvironmentTemplate == nil {
		unexpectedCall(f.t, "DeleteEnvironmentTemplate")
	}
	return f.deleteEnvironmentTemplate(a0, a1, a2)
}

func (f *fakeEnvironmentTemplates) ListEnvironmentTemplates(a0 context.Context, a1 string, a2 string, a3 int, a4 bool) (store.EnvironmentTemplatePage, error) {
	if f.listEnvironmentTemplates == nil {
		unexpectedCall(f.t, "ListEnvironmentTemplates")
	}
	return f.listEnvironmentTemplates(a0, a1, a2, a3, a4)
}

type fakeEnvironmentWorkspaces struct {
	t                        testing.TB
	readEnvironmentDirectory func(context.Context, store.Environment, string) (proto.WorkspaceDirectoryResult, error)
	writeEnvironmentFile     func(context.Context, store.Environment, string, []byte) (int64, error)
}

func (f *fakeEnvironmentWorkspaces) ReadEnvironmentDirectory(a0 context.Context, a1 store.Environment, a2 string) (proto.WorkspaceDirectoryResult, error) {
	if f.readEnvironmentDirectory == nil {
		unexpectedCall(f.t, "ReadEnvironmentDirectory")
	}
	return f.readEnvironmentDirectory(a0, a1, a2)
}

func (f *fakeEnvironmentWorkspaces) WriteEnvironmentFile(a0 context.Context, a1 store.Environment, a2 string, a3 []byte) (int64, error) {
	if f.writeEnvironmentFile == nil {
		unexpectedCall(f.t, "WriteEnvironmentFile")
	}
	return f.writeEnvironmentFile(a0, a1, a2, a3)
}

type fakeEnvironments struct {
	t                                testing.TB
	getEnvironment                   func(context.Context, string, string) (store.Environment, error)
	authorizeEnvironmentInstallation func(context.Context, identity.Principal, string, string) (string, int64, error)
	validateEnvironmentInstallation  func(context.Context, string, string) (store.InstallationAuthorization, error)
	claimEnvironmentInstallation     func(context.Context, string, string, string) error
	projectExecutorCredentialState   func(context.Context, identity.Principal, string) (store.ExecutorCredentialState, error)
	issueProjectExecutorCredential   func(context.Context, identity.Principal, string, string, bool) (store.IssuedExecutorCredential, error)
	revokeProjectExecutorCredential  func(context.Context, identity.Principal, string, string) error
}

func (f *fakeEnvironments) GetEnvironment(a0 context.Context, a1 string, a2 string) (store.Environment, error) {
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

func (f *fakeEnvironments) ValidateEnvironmentInstallation(a0 context.Context, a1 string, a2 string) (store.InstallationAuthorization, error) {
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

func (f *fakeEnvironments) ProjectExecutorCredentialState(a0 context.Context, a1 identity.Principal, a2 string) (store.ExecutorCredentialState, error) {
	if f.projectExecutorCredentialState == nil {
		unexpectedCall(f.t, "ProjectExecutorCredentialState")
	}
	return f.projectExecutorCredentialState(a0, a1, a2)
}

func (f *fakeEnvironments) IssueProjectExecutorCredential(a0 context.Context, a1 identity.Principal, a2 string, a3 string, a4 bool) (store.IssuedExecutorCredential, error) {
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

type fakeFiles struct {
	t                testing.TB
	createSourceFile func(context.Context, string, func(io.Writer) (store.SourceFileUpload, error)) (store.SourceFile, error)
	getSourceFile    func(context.Context, string, string) (store.SourceFile, error)
	listSourceFiles  func(context.Context, string, string, int, bool, *string) (store.SourceFilePage, error)
	readSourceFile   func(context.Context, string, string, func(store.SourceFile, io.Reader) error) error
	deleteSourceFile func(context.Context, string, string) error
}

func (f *fakeFiles) CreateSourceFile(a0 context.Context, a1 string, a2 func(io.Writer) (store.SourceFileUpload, error)) (store.SourceFile, error) {
	if f.createSourceFile == nil {
		unexpectedCall(f.t, "CreateSourceFile")
	}
	return f.createSourceFile(a0, a1, a2)
}

func (f *fakeFiles) GetSourceFile(a0 context.Context, a1 string, a2 string) (store.SourceFile, error) {
	if f.getSourceFile == nil {
		unexpectedCall(f.t, "GetSourceFile")
	}
	return f.getSourceFile(a0, a1, a2)
}

func (f *fakeFiles) ListSourceFiles(a0 context.Context, a1 string, a2 string, a3 int, a4 bool, a5 *string) (store.SourceFilePage, error) {
	if f.listSourceFiles == nil {
		unexpectedCall(f.t, "ListSourceFiles")
	}
	return f.listSourceFiles(a0, a1, a2, a3, a4, a5)
}

func (f *fakeFiles) ReadSourceFile(a0 context.Context, a1 string, a2 string, a3 func(store.SourceFile, io.Reader) error) error {
	if f.readSourceFile == nil {
		unexpectedCall(f.t, "ReadSourceFile")
	}
	return f.readSourceFile(a0, a1, a2, a3)
}

func (f *fakeFiles) DeleteSourceFile(a0 context.Context, a1 string, a2 string) error {
	if f.deleteSourceFile == nil {
		unexpectedCall(f.t, "DeleteSourceFile")
	}
	return f.deleteSourceFile(a0, a1, a2)
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
	t                             testing.TB
	listDeploymentModelProviders  func(context.Context) ([]store.DeploymentModelProvider, error)
	setDeploymentModelProvider    func(context.Context, string, v1.ModelConfigurationInput) (store.DeploymentModelProvider, error)
	deleteDeploymentModelProvider func(context.Context, string) error
	deploymentModelProvider       func(context.Context, string) (*store.DeploymentModelProviderSnapshot, error)
}

func (f *fakeModelProviders) ListDeploymentModelProviders(a0 context.Context) ([]store.DeploymentModelProvider, error) {
	if f.listDeploymentModelProviders == nil {
		unexpectedCall(f.t, "ListDeploymentModelProviders")
	}
	return f.listDeploymentModelProviders(a0)
}

func (f *fakeModelProviders) SetDeploymentModelProvider(a0 context.Context, a1 string, a2 v1.ModelConfigurationInput) (store.DeploymentModelProvider, error) {
	if f.setDeploymentModelProvider == nil {
		unexpectedCall(f.t, "SetDeploymentModelProvider")
	}
	return f.setDeploymentModelProvider(a0, a1, a2)
}

func (f *fakeModelProviders) DeleteDeploymentModelProvider(a0 context.Context, a1 string) error {
	if f.deleteDeploymentModelProvider == nil {
		unexpectedCall(f.t, "DeleteDeploymentModelProvider")
	}
	return f.deleteDeploymentModelProvider(a0, a1)
}

func (f *fakeModelProviders) DeploymentModelProvider(a0 context.Context, a1 string) (*store.DeploymentModelProviderSnapshot, error) {
	if f.deploymentModelProvider == nil {
		unexpectedCall(f.t, "DeploymentModelProvider")
	}
	return f.deploymentModelProvider(a0, a1)
}

type fakeProjects struct {
	t                    testing.TB
	createProject        func(context.Context, string, string) (store.Project, error)
	getProject           func(context.Context, string) (store.ProjectBinding, error)
	listProjects         func(context.Context, string, int, bool) (store.ProjectPage, error)
	renameProject        func(context.Context, string, string) (store.Project, error)
	archiveProject       func(context.Context, string) (store.Project, error)
	createProjectAPIKey  func(context.Context, string, string, string) (store.IssuedProjectAPIKey, error)
	listProjectAPIKeys   func(context.Context, string, string, int, bool) (store.ProjectAPIKeyPage, error)
	revokeProjectAPIKey  func(context.Context, string, string) error
	resolveProjectAPIKey func(context.Context, string) (store.ProjectAPIKeyBinding, error)
}

func (f *fakeProjects) CreateProject(a0 context.Context, a1 string, a2 string) (store.Project, error) {
	if f.createProject == nil {
		unexpectedCall(f.t, "CreateProject")
	}
	return f.createProject(a0, a1, a2)
}

func (f *fakeProjects) GetProject(a0 context.Context, a1 string) (store.ProjectBinding, error) {
	if f.getProject == nil {
		unexpectedCall(f.t, "GetProject")
	}
	return f.getProject(a0, a1)
}

func (f *fakeProjects) ListProjects(a0 context.Context, a1 string, a2 int, a3 bool) (store.ProjectPage, error) {
	if f.listProjects == nil {
		unexpectedCall(f.t, "ListProjects")
	}
	return f.listProjects(a0, a1, a2, a3)
}

func (f *fakeProjects) RenameProject(a0 context.Context, a1 string, a2 string) (store.Project, error) {
	if f.renameProject == nil {
		unexpectedCall(f.t, "RenameProject")
	}
	return f.renameProject(a0, a1, a2)
}

func (f *fakeProjects) ArchiveProject(a0 context.Context, a1 string) (store.Project, error) {
	if f.archiveProject == nil {
		unexpectedCall(f.t, "ArchiveProject")
	}
	return f.archiveProject(a0, a1)
}

func (f *fakeProjects) CreateProjectAPIKey(a0 context.Context, a1 string, a2 string, a3 string) (store.IssuedProjectAPIKey, error) {
	if f.createProjectAPIKey == nil {
		unexpectedCall(f.t, "CreateProjectAPIKey")
	}
	return f.createProjectAPIKey(a0, a1, a2, a3)
}

func (f *fakeProjects) ListProjectAPIKeys(a0 context.Context, a1 string, a2 string, a3 int, a4 bool) (store.ProjectAPIKeyPage, error) {
	if f.listProjectAPIKeys == nil {
		unexpectedCall(f.t, "ListProjectAPIKeys")
	}
	return f.listProjectAPIKeys(a0, a1, a2, a3, a4)
}

func (f *fakeProjects) RevokeProjectAPIKey(a0 context.Context, a1 string, a2 string) error {
	if f.revokeProjectAPIKey == nil {
		unexpectedCall(f.t, "RevokeProjectAPIKey")
	}
	return f.revokeProjectAPIKey(a0, a1, a2)
}

func (f *fakeProjects) ResolveProjectAPIKey(a0 context.Context, a1 string) (store.ProjectAPIKeyBinding, error) {
	if f.resolveProjectAPIKey == nil {
		unexpectedCall(f.t, "ResolveProjectAPIKey")
	}
	return f.resolveProjectAPIKey(a0, a1)
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
	getSessionDiagnosticsSnapshot    func(context.Context, string, string) (store.Session, error)
	getTurnDiagnosticsSnapshot       func(context.Context, string, string, string) (store.TurnDiagnosticsSnapshot, error)
	getSessionExecutionConfiguration func(context.Context, string, string) (v1.SessionExecutionConfiguration, error)
	getManagedSessionArchive         func(context.Context, string, string) (store.ManagedSessionArchive, error)
}

func (f *fakeSessionAdmin) GetSessionDiagnosticsSnapshot(a0 context.Context, a1 string, a2 string) (store.Session, error) {
	if f.getSessionDiagnosticsSnapshot == nil {
		unexpectedCall(f.t, "GetSessionDiagnosticsSnapshot")
	}
	return f.getSessionDiagnosticsSnapshot(a0, a1, a2)
}

func (f *fakeSessionAdmin) GetTurnDiagnosticsSnapshot(a0 context.Context, a1 string, a2 string, a3 string) (store.TurnDiagnosticsSnapshot, error) {
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

func (f *fakeSessionAdmin) GetManagedSessionArchive(a0 context.Context, a1 string, a2 string) (store.ManagedSessionArchive, error) {
	if f.getManagedSessionArchive == nil {
		unexpectedCall(f.t, "GetManagedSessionArchive")
	}
	return f.getManagedSessionArchive(a0, a1, a2)
}

type fakeSessionArchive struct {
	t                     testing.TB
	archiveManagedSession func(context.Context, string, string, uint64) (store.ManagedSessionArchive, error)
}

func (f *fakeSessionArchive) ArchiveManagedSession(a0 context.Context, a1 string, a2 string, a3 uint64) (store.ManagedSessionArchive, error) {
	if f.archiveManagedSession == nil {
		unexpectedCall(f.t, "ArchiveManagedSession")
	}
	return f.archiveManagedSession(a0, a1, a2, a3)
}

type fakeSessionEvents struct {
	t                     testing.TB
	sessionEventCursor    func(context.Context, string, string) (int64, error)
	listSessionEvents     func(context.Context, string, string, int64) ([]store.SessionChange, error)
	sessionStreamSnapshot func(context.Context, string, string) (store.Session, int64, error)
}

func (f *fakeSessionEvents) SessionEventCursor(a0 context.Context, a1 string, a2 string) (int64, error) {
	if f.sessionEventCursor == nil {
		unexpectedCall(f.t, "SessionEventCursor")
	}
	return f.sessionEventCursor(a0, a1, a2)
}

func (f *fakeSessionEvents) ListSessionEvents(a0 context.Context, a1 string, a2 string, a3 int64) ([]store.SessionChange, error) {
	if f.listSessionEvents == nil {
		unexpectedCall(f.t, "ListSessionEvents")
	}
	return f.listSessionEvents(a0, a1, a2, a3)
}

func (f *fakeSessionEvents) SessionStreamSnapshot(a0 context.Context, a1 string, a2 string) (store.Session, int64, error) {
	if f.sessionStreamSnapshot == nil {
		unexpectedCall(f.t, "SessionStreamSnapshot")
	}
	return f.sessionStreamSnapshot(a0, a1, a2)
}

type fakeSessionHistory struct {
	t         testing.TB
	getTurn   func(context.Context, string, string, string) (store.Turn, error)
	listTurns func(context.Context, string, string, string, int, bool) (store.TurnPage, error)
	listItems func(context.Context, string, string, string, int, bool) (store.ItemPage, error)
}

func (f *fakeSessionHistory) GetTurn(a0 context.Context, a1 string, a2 string, a3 string) (store.Turn, error) {
	if f.getTurn == nil {
		unexpectedCall(f.t, "GetTurn")
	}
	return f.getTurn(a0, a1, a2, a3)
}

func (f *fakeSessionHistory) ListTurns(a0 context.Context, a1 string, a2 string, a3 string, a4 int, a5 bool) (store.TurnPage, error) {
	if f.listTurns == nil {
		unexpectedCall(f.t, "ListTurns")
	}
	return f.listTurns(a0, a1, a2, a3, a4, a5)
}

func (f *fakeSessionHistory) ListItems(a0 context.Context, a1 string, a2 string, a3 string, a4 int, a5 bool) (store.ItemPage, error) {
	if f.listItems == nil {
		unexpectedCall(f.t, "ListItems")
	}
	return f.listItems(a0, a1, a2, a3, a4, a5)
}

type fakeSessions struct {
	t                     testing.TB
	createSession         func(context.Context, string, store.CreateSessionInput) (store.Session, error)
	createSessionStream   func(context.Context, string, store.CreateSessionInput) (store.SessionCreation, error)
	findSessionCreation   func(context.Context, string, string, json.RawMessage, identity.Subject) (store.SessionCreation, error)
	getSession            func(context.Context, string, string) (store.Session, error)
	listSessions          func(context.Context, string, string, int, bool, *string) (store.SessionPage, error)
	updateSessionMetadata func(context.Context, string, string, map[string]string) (store.Session, error)
	deleteSession         func(context.Context, string, string) error
	auditSessionOperation func(context.Context, string, string, string) error
}

func (f *fakeSessions) CreateSession(a0 context.Context, a1 string, a2 store.CreateSessionInput) (store.Session, error) {
	if f.createSession == nil {
		unexpectedCall(f.t, "CreateSession")
	}
	return f.createSession(a0, a1, a2)
}

func (f *fakeSessions) CreateSessionStream(a0 context.Context, a1 string, a2 store.CreateSessionInput) (store.SessionCreation, error) {
	if f.createSessionStream == nil {
		unexpectedCall(f.t, "CreateSessionStream")
	}
	return f.createSessionStream(a0, a1, a2)
}

func (f *fakeSessions) FindSessionCreation(a0 context.Context, a1 string, a2 string, a3 json.RawMessage, a4 identity.Subject) (store.SessionCreation, error) {
	if f.findSessionCreation == nil {
		unexpectedCall(f.t, "FindSessionCreation")
	}
	return f.findSessionCreation(a0, a1, a2, a3, a4)
}

func (f *fakeSessions) GetSession(a0 context.Context, a1 string, a2 string) (store.Session, error) {
	if f.getSession == nil {
		unexpectedCall(f.t, "GetSession")
	}
	return f.getSession(a0, a1, a2)
}

func (f *fakeSessions) ListSessions(a0 context.Context, a1 string, a2 string, a3 int, a4 bool, a5 *string) (store.SessionPage, error) {
	if f.listSessions == nil {
		unexpectedCall(f.t, "ListSessions")
	}
	return f.listSessions(a0, a1, a2, a3, a4, a5)
}

func (f *fakeSessions) UpdateSessionMetadata(a0 context.Context, a1 string, a2 string, a3 map[string]string) (store.Session, error) {
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
	t                       testing.TB
	createSkill             func(context.Context, string, []byte) (store.Skill, error)
	getSkill                func(context.Context, string, string) (store.Skill, error)
	updateSkillDefault      func(context.Context, string, string, string) (store.Skill, error)
	deleteSkill             func(context.Context, string, string) error
	listSkills              func(context.Context, string, string, int, bool) (store.SkillPage, error)
	createSkillVersion      func(context.Context, string, string, []byte, bool) (store.SkillVersion, error)
	getSkillVersion         func(context.Context, string, string, string) (store.SkillVersion, error)
	readSkillVersion        func(context.Context, string, string, string) (store.SkillVersion, []byte, error)
	readDefaultSkillVersion func(context.Context, string, string) (store.SkillVersion, []byte, error)
	deleteSkillVersion      func(context.Context, string, string, string) (store.SkillVersion, error)
	listSkillVersions       func(context.Context, string, string, string, int, bool) (store.SkillVersionPage, error)
}

func (f *fakeSkills) CreateSkill(a0 context.Context, a1 string, a2 []byte) (store.Skill, error) {
	if f.createSkill == nil {
		unexpectedCall(f.t, "CreateSkill")
	}
	return f.createSkill(a0, a1, a2)
}

func (f *fakeSkills) GetSkill(a0 context.Context, a1 string, a2 string) (store.Skill, error) {
	if f.getSkill == nil {
		unexpectedCall(f.t, "GetSkill")
	}
	return f.getSkill(a0, a1, a2)
}

func (f *fakeSkills) UpdateSkillDefault(a0 context.Context, a1 string, a2 string, a3 string) (store.Skill, error) {
	if f.updateSkillDefault == nil {
		unexpectedCall(f.t, "UpdateSkillDefault")
	}
	return f.updateSkillDefault(a0, a1, a2, a3)
}

func (f *fakeSkills) DeleteSkill(a0 context.Context, a1 string, a2 string) error {
	if f.deleteSkill == nil {
		unexpectedCall(f.t, "DeleteSkill")
	}
	return f.deleteSkill(a0, a1, a2)
}

func (f *fakeSkills) ListSkills(a0 context.Context, a1 string, a2 string, a3 int, a4 bool) (store.SkillPage, error) {
	if f.listSkills == nil {
		unexpectedCall(f.t, "ListSkills")
	}
	return f.listSkills(a0, a1, a2, a3, a4)
}

func (f *fakeSkills) CreateSkillVersion(a0 context.Context, a1 string, a2 string, a3 []byte, a4 bool) (store.SkillVersion, error) {
	if f.createSkillVersion == nil {
		unexpectedCall(f.t, "CreateSkillVersion")
	}
	return f.createSkillVersion(a0, a1, a2, a3, a4)
}

func (f *fakeSkills) GetSkillVersion(a0 context.Context, a1 string, a2 string, a3 string) (store.SkillVersion, error) {
	if f.getSkillVersion == nil {
		unexpectedCall(f.t, "GetSkillVersion")
	}
	return f.getSkillVersion(a0, a1, a2, a3)
}

func (f *fakeSkills) ReadSkillVersion(a0 context.Context, a1 string, a2 string, a3 string) (store.SkillVersion, []byte, error) {
	if f.readSkillVersion == nil {
		unexpectedCall(f.t, "ReadSkillVersion")
	}
	return f.readSkillVersion(a0, a1, a2, a3)
}

func (f *fakeSkills) ReadDefaultSkillVersion(a0 context.Context, a1 string, a2 string) (store.SkillVersion, []byte, error) {
	if f.readDefaultSkillVersion == nil {
		unexpectedCall(f.t, "ReadDefaultSkillVersion")
	}
	return f.readDefaultSkillVersion(a0, a1, a2)
}

func (f *fakeSkills) DeleteSkillVersion(a0 context.Context, a1 string, a2 string, a3 string) (store.SkillVersion, error) {
	if f.deleteSkillVersion == nil {
		unexpectedCall(f.t, "DeleteSkillVersion")
	}
	return f.deleteSkillVersion(a0, a1, a2, a3)
}

func (f *fakeSkills) ListSkillVersions(a0 context.Context, a1 string, a2 string, a3 string, a4 int, a5 bool) (store.SkillVersionPage, error) {
	if f.listSkillVersions == nil {
		unexpectedCall(f.t, "ListSkillVersions")
	}
	return f.listSkillVersions(a0, a1, a2, a3, a4, a5)
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

type fakeVaults struct {
	t                      testing.TB
	createVault            func(context.Context, string, store.CreateVaultInput) (store.Vault, error)
	getVault               func(context.Context, string, string) (store.Vault, error)
	deleteVault            func(context.Context, string, string) (string, error)
	listVaults             func(context.Context, string, string, int, bool, []string) (store.VaultPage, error)
	createOAuthCredential  func(context.Context, string, string, store.CreateOAuthCredentialInput) (store.Credential, error)
	updateOAuthCredential  func(context.Context, string, string, string, store.UpdateOAuthCredentialInput) (store.Credential, error)
	createStaticCredential func(context.Context, string, string, store.CreateStaticCredentialInput) (store.Credential, error)
	updateStaticCredential func(context.Context, string, string, string, store.UpdateStaticCredentialInput) (store.Credential, error)
	getCredential          func(context.Context, string, string, string) (store.Credential, error)
	deleteCredential       func(context.Context, string, string, string) (string, error)
	listCredentials        func(context.Context, string, string, string, int, bool, []string) (store.CredentialPage, error)
	resolveMCPCredentials  func(context.Context, string, []string, []store.MCPCredentialRequest) ([]store.MCPCredentialBinding, error)
}

func (f *fakeVaults) CreateVault(a0 context.Context, a1 string, a2 store.CreateVaultInput) (store.Vault, error) {
	if f.createVault == nil {
		unexpectedCall(f.t, "CreateVault")
	}
	return f.createVault(a0, a1, a2)
}

func (f *fakeVaults) GetVault(a0 context.Context, a1 string, a2 string) (store.Vault, error) {
	if f.getVault == nil {
		unexpectedCall(f.t, "GetVault")
	}
	return f.getVault(a0, a1, a2)
}

func (f *fakeVaults) DeleteVault(a0 context.Context, a1 string, a2 string) (string, error) {
	if f.deleteVault == nil {
		unexpectedCall(f.t, "DeleteVault")
	}
	return f.deleteVault(a0, a1, a2)
}

func (f *fakeVaults) ListVaults(a0 context.Context, a1 string, a2 string, a3 int, a4 bool, a5 []string) (store.VaultPage, error) {
	if f.listVaults == nil {
		unexpectedCall(f.t, "ListVaults")
	}
	return f.listVaults(a0, a1, a2, a3, a4, a5)
}

func (f *fakeVaults) CreateOAuthCredential(a0 context.Context, a1 string, a2 string, a3 store.CreateOAuthCredentialInput) (store.Credential, error) {
	if f.createOAuthCredential == nil {
		unexpectedCall(f.t, "CreateOAuthCredential")
	}
	return f.createOAuthCredential(a0, a1, a2, a3)
}

func (f *fakeVaults) UpdateOAuthCredential(a0 context.Context, a1 string, a2 string, a3 string, a4 store.UpdateOAuthCredentialInput) (store.Credential, error) {
	if f.updateOAuthCredential == nil {
		unexpectedCall(f.t, "UpdateOAuthCredential")
	}
	return f.updateOAuthCredential(a0, a1, a2, a3, a4)
}

func (f *fakeVaults) CreateStaticCredential(a0 context.Context, a1 string, a2 string, a3 store.CreateStaticCredentialInput) (store.Credential, error) {
	if f.createStaticCredential == nil {
		unexpectedCall(f.t, "CreateStaticCredential")
	}
	return f.createStaticCredential(a0, a1, a2, a3)
}

func (f *fakeVaults) UpdateStaticCredential(a0 context.Context, a1 string, a2 string, a3 string, a4 store.UpdateStaticCredentialInput) (store.Credential, error) {
	if f.updateStaticCredential == nil {
		unexpectedCall(f.t, "UpdateStaticCredential")
	}
	return f.updateStaticCredential(a0, a1, a2, a3, a4)
}

func (f *fakeVaults) GetCredential(a0 context.Context, a1 string, a2 string, a3 string) (store.Credential, error) {
	if f.getCredential == nil {
		unexpectedCall(f.t, "GetCredential")
	}
	return f.getCredential(a0, a1, a2, a3)
}

func (f *fakeVaults) DeleteCredential(a0 context.Context, a1 string, a2 string, a3 string) (string, error) {
	if f.deleteCredential == nil {
		unexpectedCall(f.t, "DeleteCredential")
	}
	return f.deleteCredential(a0, a1, a2, a3)
}

func (f *fakeVaults) ListCredentials(a0 context.Context, a1 string, a2 string, a3 string, a4 int, a5 bool, a6 []string) (store.CredentialPage, error) {
	if f.listCredentials == nil {
		unexpectedCall(f.t, "ListCredentials")
	}
	return f.listCredentials(a0, a1, a2, a3, a4, a5, a6)
}

func (f *fakeVaults) ResolveMCPCredentials(a0 context.Context, a1 string, a2 []string, a3 []store.MCPCredentialRequest) ([]store.MCPCredentialBinding, error) {
	if f.resolveMCPCredentials == nil {
		unexpectedCall(f.t, "ResolveMCPCredentials")
	}
	return f.resolveMCPCredentials(a0, a1, a2, a3)
}

type fakeWriteAudit struct {
	t                   testing.TB
	getResourceOwners   func(context.Context, string, string, []string) ([]store.ResourceOwner, error)
	listWriteOperations func(context.Context, string, store.WriteOperationFilter) (store.WriteOperationPage, error)
}

func (f *fakeWriteAudit) GetResourceOwners(a0 context.Context, a1 string, a2 string, a3 []string) ([]store.ResourceOwner, error) {
	if f.getResourceOwners == nil {
		unexpectedCall(f.t, "GetResourceOwners")
	}
	return f.getResourceOwners(a0, a1, a2, a3)
}

func (f *fakeWriteAudit) ListWriteOperations(a0 context.Context, a1 string, a2 store.WriteOperationFilter) (store.WriteOperationPage, error) {
	if f.listWriteOperations == nil {
		unexpectedCall(f.t, "ListWriteOperations")
	}
	return f.listWriteOperations(a0, a1, a2)
}
