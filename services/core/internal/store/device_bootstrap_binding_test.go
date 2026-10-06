package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/google/uuid"
)

func TestDeviceCredentialCarriesPersistedAllocationNode(t *testing.T) {
	s, writer, d := managerFixture(t, 4, 8)
	nodes := deploymentService(t, s)
	token, err := EnrollmentTestToken(nodes.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 4, MaxRetained: 8}))
	if err != nil {
		t.Fatal(err)
	}
	remote := uuid.NewString()
	_, err = nodes.Enroll(t.Context(), token, deployment.Enrollment{DeploymentGeneration: 1, SpecificationDigest: SandboxDeploymentTestSpec("docker").Digest("docker"), NodeID: remote, Credential: strings.Repeat("x", 64),
		Name: "remote", Provider: "docker", BackendFingerprint: strings.Repeat("b", 64), CoreURL: s.placement.PublicURL()})
	if err != nil {
		t.Fatal(err)
	}
	onlineManagerNode(t, s, remote)
	for _, nodeID := range []string{d.LocalNodeID, remote} {
		t.Run(nodeID, func(t *testing.T) {
			tenant, bearer := uuid.NewString(), uuid.NewString()
			session, err := createSessionOnNode(t, s, tenant, managerSessionInput(uuid.NewString()), nodeID)
			if err != nil {
				t.Fatal(err)
			}
			environment, err := sessionAdapter(s).GetSessionEnvironment(t.Context(), tenant, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			allocation, err := deploymentExecution(t, writer).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID}, d.InstallationID, runtimedevice.HashCredential(bearer))
			if err != nil {
				t.Fatal(err)
			}
			authenticator := runtimegateway.NewAuthenticator(sessionAdapter(s))
			auth, err := authenticator.AuthenticateBearer(t.Context(), allocation.DeviceID, bearer)
			if err != nil || auth.RuntimeNodeID != nodeID {
				t.Fatalf("authenticated node=%s want=%s error=%v", auth.RuntimeNodeID, nodeID, err)
			}
			if _, err := authenticator.AuthenticateBearer(t.Context(), allocation.DeviceID, "wrong-token"); !errors.Is(err, runtimegateway.ErrAuthBadCredential) {
				t.Fatal("binding bypassed credential check", err)
			}
			if err := sessionService(t, s).RevokeDevice(t.Context(), tenant, allocation.DeviceID); err != nil {
				t.Fatal(err)
			}
			if _, err := authenticator.AuthenticateBearer(t.Context(), allocation.DeviceID, bearer); !errors.Is(err, runtimegateway.ErrAuthUnknownDevice) {
				t.Fatal("allocation revived revoked credential", err)
			}
		})
	}
}

func TestDeviceCredentialWithoutManagedNodeRetainsPublicRouteIdentity(t *testing.T) {
	s, _ := testStore(t)
	tenant := uuid.NewString()
	ordinary, err := sessionService(t, s).CreateDevice(t.Context(), tenant, "ordinary", runtimedevice.HashCredential("ordinary-token"))
	if err != nil {
		t.Fatal(err)
	}
	_, environment := localEnvironment(t, s, tenant)
	allocation, err := deploymentExecution(t, executionWriter(t, s)).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID}, uuid.NewString(), runtimedevice.HashCredential("allocation-token"))
	if err != nil {
		t.Fatal(err)
	}
	principal := FixtureExecutorPrincipal(t, s, uuid.NewString())
	_, selfhost, key := runtimeEnrollmentFixture(t, s, principal)
	enrolled, err := sessionService(t, s).EnrollRuntime(t.Context(), selfhost.ID, executorDigest(key.Token))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ordinary.ID, allocation.DeviceID, enrolled.DeviceID} {
		credential, found, err := sessionAdapter(s).GetDeviceCredential(t.Context(), id)
		if err != nil || !found || credential.RuntimeNodeID != "" {
			t.Fatalf("non-node credential acquired allocation route: found=%v node=%s error=%v", found, credential.RuntimeNodeID, err)
		}
	}
	if err := sessionService(t, s).RevokeExecutorCredential(t.Context(), principal, key.KeyID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := sessionAdapter(s).GetDeviceCredential(t.Context(), enrolled.DeviceID); err != nil || found {
		t.Fatal("LEFT JOIN revived revoked executor key", err)
	}
}
