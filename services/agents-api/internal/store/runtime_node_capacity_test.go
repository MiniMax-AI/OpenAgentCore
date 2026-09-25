package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRuntimeEnrollmentApprovedCapacity(t *testing.T) {
	s, _, view, _ := webSpecificationFixture(t, "docker")
	for _, capacity := range []RuntimeNodeCapacity{{0, 8}, {3, 2}, {1, 1000001}} {
		if _, _, err := s.CreateRuntimeEnrollment(t.Context(), capacity); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid capacity accepted", err)
		}
	}
	token, _, err := s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{1, 3})
	if err != nil {
		t.Fatal(err)
	}
	config, err := s.RuntimeNodeConfiguration(t.Context(), "", token)
	if err != nil || config.MaxActive != 1 || config.MaxRetained != 3 {
		t.Fatal("bootstrap lost approved capacity", config, err)
	}
	input := RuntimeNodeEnrollment{NodeID: uuid.NewString(), Name: "approved", Credential: strings.Repeat("a", 64), Provider: "docker", BackendFingerprint: strings.Repeat("b", 64), SpecificationDigest: view.SpecificationDigest, DeploymentGeneration: view.Generation}
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
	identity, err = s.AuthenticateRuntimeNode(t.Context(), input.NodeID, input.Credential)
	if err != nil || identity.MaxActive != 2 || identity.MaxRetained != 5 {
		t.Fatal("credential read ignored admin update", identity, err)
	}
	config, err = s.RuntimeNodeConfiguration(t.Context(), input.NodeID, input.Credential)
	if err != nil || config.MaxActive != 2 || config.MaxRetained != 5 {
		t.Fatal("configuration read ignored admin update", config, err)
	}
}
