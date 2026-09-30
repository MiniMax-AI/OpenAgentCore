package adminaudit

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidateMutation(t *testing.T) {
	project := Source{CredentialID: "12345678", ActorLabel: "console", RequestID: "request", TraceID: "trace", ProjectID: "project"}
	deployment := project
	deployment.ProjectID = ""
	if err := project.ValidateProjectMutation("update", "agent", "agent"); err != nil {
		t.Fatal(err)
	}
	if err := deployment.ValidateDeploymentMutation("set", "deployment_model_provider", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := deployment.ValidateProjectMutation("update", "agent", "agent"); !errors.Is(err, ErrInvalidSource) {
		t.Fatal("Project mutation without a Project accepted", err)
	}
	if err := project.ValidateDeploymentMutation("set", "deployment_model_provider", "codex"); !errors.Is(err, ErrInvalidSource) {
		t.Fatal("deployment mutation with a Project accepted", err)
	}
	for name, change := range map[string]func(*Source, *[3]string){
		"credential": func(s *Source, _ *[3]string) { s.CredentialID = "" },
		"actor":      func(s *Source, _ *[3]string) { s.ActorLabel = strings.Repeat("x", 129) },
		"request":    func(s *Source, _ *[3]string) { s.RequestID = "" },
		"trace":      func(s *Source, _ *[3]string) { s.TraceID = "bad\x01" },
		"action":     func(_ *Source, r *[3]string) { r[0] = "" },
		"type":       func(_ *Source, r *[3]string) { r[1] = strings.Repeat("x", 65) },
		"resource":   func(_ *Source, r *[3]string) { r[2] = "" },
	} {
		source, record := project, [3]string{"update", "agent", "agent"}
		change(&source, &record)
		if err := source.ValidateProjectMutation(record[0], record[1], record[2]); !errors.Is(err, ErrInvalidSource) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
}

func TestFilterValidate(t *testing.T) {
	if f, err := (Filter{}).Validate(); err != nil || f.Limit != 50 {
		t.Fatal(f, err)
	}
	now := time.Now()
	for _, f := range []Filter{{Limit: 101}, {ProjectID: strings.Repeat("x", 129)}, {Action: "bad\x00"}, {CreatedAfter: &now, CreatedBefore: &now}} {
		if _, err := f.Validate(); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("filter %+v accepted", f)
		}
	}
}
