package store

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestTemplateNetworkPolicyRoundTripAndReplacement(t *testing.T) {
	s, _ := testStore(t)
	ctx := t.Context()
	tenant, foreign := uuid.NewString(), uuid.NewString()
	domains := []string{"Example.com", "api.example.com", "Example.com"}
	created, err := s.CreateEnvironmentTemplate(ctx, tenant, EnvironmentTemplateInput{SetNetwork: true, NetworkAccess: "restricted", AllowedDomains: domains})
	if err != nil {
		t.Fatal(err)
	}
	check := func(value EnvironmentTemplate, err error) {
		t.Helper()
		if err != nil || value.NetworkAccess != "restricted" || !reflect.DeepEqual(value.AllowedDomains, domains) {
			t.Fatalf("network policy lost: %#v, %v", value, err)
		}
	}
	check(created, nil)
	check(s.GetEnvironmentTemplate(ctx, tenant, created.ID))
	resolved, _, err := s.ResolveEnvironmentTemplate(ctx, tenant, created.ID)
	check(resolved, err)
	page, err := s.ListEnvironmentTemplates(ctx, tenant, "", 1, true)
	if err != nil || len(page.Templates) != 1 {
		t.Fatal(page, err)
	}
	check(page.Templates[0], nil)
	name := "renamed"
	check(s.UpdateEnvironmentTemplate(ctx, tenant, created.ID, EnvironmentTemplateInput{SetName: true, Name: &name}))
	if _, _, err := s.ResolveEnvironmentTemplate(ctx, foreign, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign policy resolution", err)
	}
	for _, in := range []EnvironmentTemplateInput{
		{SetNetwork: true, NetworkAccess: "enabled", AllowedDomains: domains},
		{SetNetwork: true, NetworkAccess: "restricted"},
		{SetNetwork: true, NetworkAccess: "restricted", AllowedDomains: []string{"*.example.com"}},
	} {
		if _, err := s.UpdateEnvironmentTemplate(ctx, tenant, created.ID, in); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid policy replacement", err)
		}
		check(s.GetEnvironmentTemplate(ctx, tenant, created.ID))
	}
	domains = []string{"other.example.com"}
	check(s.UpdateEnvironmentTemplate(ctx, tenant, created.ID, EnvironmentTemplateInput{SetNetwork: true, NetworkAccess: "restricted", AllowedDomains: domains}))
	for _, access := range []string{"disabled", "enabled"} {
		value, err := s.UpdateEnvironmentTemplate(ctx, tenant, created.ID, EnvironmentTemplateInput{SetNetwork: true, NetworkAccess: access})
		if err != nil || value.NetworkAccess != access || value.AllowedDomains == nil || len(value.AllowedDomains) != 0 {
			t.Fatal("policy reset", value, err)
		}
	}
}
