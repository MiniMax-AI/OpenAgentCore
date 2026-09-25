package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRuntimeEnrollmentApprovedCapacity(t *testing.T) {
	s, _, view, _ := webSpecificationFixture(t, "microsandbox")
	for _, capacity := range []RuntimeNodeCapacity{{0, 8}, {3, 2}, {1, 1000001}} {
		if _, err := s.CreateRuntimeEnrollment(t.Context(), capacity); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid capacity accepted", err)
		}
	}
	token, err := EnrollmentTestToken(s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{1, 3}))
	if err != nil {
		t.Fatal(err)
	}
	config, err := s.RuntimeNodeConfiguration(t.Context(), "", token)
	if err != nil || config.MaxActive != 1 || config.MaxRetained != 3 {
		t.Fatal("bootstrap lost approved capacity", config, err)
	}
	input := RuntimeNodeEnrollment{NodeID: uuid.NewString(), Name: "approved", Credential: strings.Repeat("a", 64), Provider: "microsandbox", BackendFingerprint: strings.Repeat("b", 64), SpecificationDigest: view.SpecificationDigest, DeploymentGeneration: view.Generation}
	identity, err := s.EnrollRuntimeNode(t.Context(), token, input)
	if err != nil || identity.MaxActive != 1 || identity.MaxRetained != 3 {
		t.Fatal("enrollment did not apply token capacity", identity, err)
	}
	if _, err := s.EnrollRuntimeNode(t.Context(), token, input); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal("consumed token reused", err)
	}
	if err := s.UpdateRuntimeNode(t.Context(), input.NodeID, RuntimeNodeUpdate{Name: "approved", MaxActive: 2, MaxRetained: 5}); err != nil {
		t.Fatal(err)
	}
	// Microsandbox uses both limits, so a retained limit below the active one is rejected and nothing is written.
	if err := s.UpdateRuntimeNode(t.Context(), input.NodeID, RuntimeNodeUpdate{Name: "rejected", MaxActive: 4, MaxRetained: 3}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("retained limit below active accepted", err)
	}
	var name string
	if err := s.pool.QueryRow(t.Context(), "SELECT name FROM runtime_nodes WHERE id=$1", input.NodeID).Scan(&name); err != nil || name != "approved" {
		t.Fatal("rejected update was written", name, err)
	}
	identity, err = s.AuthenticateRuntimeNode(t.Context(), input.NodeID, input.Credential)
	if err != nil || identity.MaxActive != 2 || identity.MaxRetained != 5 {
		t.Fatal("credential read ignored admin update", identity, err)
	}
	config, err = s.RuntimeNodeConfiguration(t.Context(), input.NodeID, input.Credential)
	if err != nil || config.MaxActive != 2 || config.MaxRetained != 5 {
		t.Fatal("configuration read ignored admin update", config, err)
	}
	// Invalid capacity is reported before the deployment's maintenance conflict.
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_deployment SET maintenance=true"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{3, 2}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("invalid capacity reported as a conflict", err)
	}
}

// Docker never suspends a sandbox, so every read reports its retained limit as its
// active limit, including for a token or node stored before Core applied that rule.
func TestDockerRetainedLimitFollowsActive(t *testing.T) {
	s, _, view, _ := webSpecificationFixture(t, "docker")
	node := func(name, credential string) RuntimeNodeEnrollment {
		return RuntimeNodeEnrollment{NodeID: uuid.NewString(), Name: name, Credential: strings.Repeat(credential, 64), Provider: "docker", BackendFingerprint: strings.Repeat("b", 64), SpecificationDigest: view.SpecificationDigest, DeploymentGeneration: view.Generation}
	}
	sql := func(query string, args ...any) {
		t.Helper()
		if _, err := s.pool.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	token, err := EnrollmentTestToken(s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{MaxActive: 3, MaxRetained: 1}))
	if err != nil {
		t.Fatal(err)
	}
	current := node("Docker", "d")
	if identity, err := s.EnrollRuntimeNode(t.Context(), token, current); err != nil || identity.MaxRetained != 3 {
		t.Fatal("enrollment kept a separate Docker retained limit", identity, err)
	}
	legacyToken, err := EnrollmentTestToken(s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{MaxActive: 2, MaxRetained: 8}))
	if err != nil {
		t.Fatal(err)
	}
	sql("UPDATE runtime_node_enrollments SET max_retained=8 WHERE token_sha256=$1", runtimeTokenDigest(legacyToken))
	if config, err := s.RuntimeNodeConfiguration(t.Context(), "", legacyToken); err != nil || config.MaxRetained != 2 {
		t.Fatal("bootstrap reported a legacy Docker retained limit", config, err)
	}
	legacy := node("Legacy", "l")
	if _, err := s.EnrollRuntimeNode(t.Context(), legacyToken, legacy); err != nil {
		t.Fatal(err)
	}
	var stored int32
	if err := s.pool.QueryRow(t.Context(), "SELECT max_retained FROM runtime_nodes WHERE id=$1", legacy.NodeID).Scan(&stored); err != nil || stored != 2 {
		t.Fatal("enrollment stored a legacy Docker retained limit", stored, err)
	}
	sql("UPDATE runtime_nodes SET max_retained=8 WHERE id=$1", legacy.NodeID)
	if identity, err := s.AuthenticateRuntimeNode(t.Context(), legacy.NodeID, legacy.Credential); err != nil || identity.MaxRetained != 2 {
		t.Fatal("node identity reported a legacy Docker retained limit", identity, err)
	}
	if config, err := s.RuntimeNodeConfiguration(t.Context(), legacy.NodeID, legacy.Credential); err != nil || config.MaxRetained != 2 {
		t.Fatal("node configuration reported a legacy Docker retained limit", config, err)
	}
	if err := s.UpdateRuntimeNode(t.Context(), current.NodeID, RuntimeNodeUpdate{Name: "Docker", MaxActive: 5, MaxRetained: 12}); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.ListRuntimeNodes(t.Context())
	if err != nil || len(nodes) != 2 {
		t.Fatal(nodes, err)
	}
	for _, n := range nodes {
		if n.MaxRetained != n.MaxActive || (n.ID == current.NodeID && n.MaxActive != 5) {
			t.Fatal("node list kept a separate Docker retained limit", n)
		}
	}
}
