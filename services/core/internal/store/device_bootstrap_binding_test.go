package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/gateway"
	"github.com/google/uuid"
)

func TestDeviceCredentialCarriesPersistedAllocationNode(t *testing.T) {
	s, writer, deployment := managerFixture(t, 4, 8)
	token, err := EnrollmentTestToken(s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{MaxActive: 4, MaxRetained: 8}))
	if err != nil {
		t.Fatal(err)
	}
	remote := uuid.NewString()
	_, err = s.EnrollRuntimeNode(t.Context(), token, RuntimeNodeEnrollment{DeploymentGeneration: 1, SpecificationDigest: SandboxDeploymentTestSpec("docker").Digest("docker"), NodeID: remote, Credential: strings.Repeat("x", 64),
		Name: "remote", Provider: "docker", BackendFingerprint: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	onlineManagerNode(t, s, remote)
	for _, nodeID := range []string{deployment.LocalNodeID, remote} {
		t.Run(nodeID, func(t *testing.T) {
			tenant, bearer := uuid.NewString(), uuid.NewString()
			session, err := createSessionOnNode(t, s, tenant, managerSessionInput(uuid.NewString()), nodeID)
			if err != nil {
				t.Fatal(err)
			}
			environment, err := s.GetSessionEnvironment(t.Context(), tenant, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			allocation, err := writer.ReserveRuntimeAllocation(t.Context(), tenant, environment.ID, deployment.InstallationID, device.HashCredential(bearer))
			if err != nil {
				t.Fatal(err)
			}
			authenticator := gateway.NewAuthenticator(s)
			auth, err := authenticator.AuthenticateBearer(t.Context(), allocation.DeviceID, bearer)
			if err != nil || auth.RuntimeNodeID != nodeID {
				t.Fatalf("authenticated node=%s want=%s error=%v", auth.RuntimeNodeID, nodeID, err)
			}
			if _, err := authenticator.AuthenticateBearer(t.Context(), allocation.DeviceID, "wrong-token"); !errors.Is(err, gateway.ErrAuthBadCredential) {
				t.Fatal("binding bypassed credential check", err)
			}
			if err := s.RevokeDevice(t.Context(), tenant, allocation.DeviceID); err != nil {
				t.Fatal(err)
			}
			if _, err := authenticator.AuthenticateBearer(t.Context(), allocation.DeviceID, bearer); !errors.Is(err, gateway.ErrAuthUnknownDevice) {
				t.Fatal("allocation revived revoked credential", err)
			}
		})
	}
}

func TestDeviceCredentialWithoutManagedNodeRetainsPublicRouteIdentity(t *testing.T) {
	s, _ := testStore(t)
	tenant := uuid.NewString()
	ordinary, err := s.CreateDevice(t.Context(), tenant, "ordinary", device.HashCredential("ordinary-token"))
	if err != nil {
		t.Fatal(err)
	}
	_, environment := localEnvironment(t, s, tenant)
	allocation, err := executionLease(t, s).Store().ReserveRuntimeAllocation(t.Context(), tenant, environment.ID, uuid.NewString(), device.HashCredential("allocation-token"))
	if err != nil {
		t.Fatal(err)
	}
	principal := FixtureExecutorPrincipal(t, s, uuid.NewString())
	_, selfhost, key := runtimeEnrollmentFixture(t, s, principal)
	enrolled, err := s.EnrollRuntime(t.Context(), selfhost.ID, executorDigest(key.Token))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ordinary.ID, allocation.DeviceID, enrolled.DeviceID} {
		credential, found, err := s.GetDeviceCredential(t.Context(), id)
		if err != nil || !found || credential.RuntimeNodeID != "" {
			t.Fatalf("non-node credential acquired allocation route: found=%v node=%s error=%v", found, credential.RuntimeNodeID, err)
		}
	}
	if err := s.RevokeExecutorCredential(t.Context(), principal, key.KeyID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.GetDeviceCredential(t.Context(), enrolled.DeviceID); err != nil || found {
		t.Fatal("LEFT JOIN revived revoked executor key", err)
	}
}
