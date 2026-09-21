package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestSkillReferenceIdentityStopsAtCoreBoundary(t *testing.T) {
	session := store.Session{ID: "session", TenantID: "tenant"}
	environment := store.Environment{ID: "environment", SessionID: session.ID, TenantID: session.TenantID,
		Configuration: []byte(`{"type":"openai_hosted","initialization":true,"skills":[{"type":"skill_reference","skill_id":"skill-private","version":"1","name":"proof","description":"A proof."}]}`)}
	var request proto.PromptRequestPayload
	_, err := (&Dispatcher{}).configurePreparedEnvironment(t.Context(), session, environment, store.ExecutionDevice{EnvironmentID: environment.ID}, &request)
	if err != nil || request.LocalEnvironment == nil || !request.LocalEnvironment.Capabilities || len(request.LocalEnvironment.Skills) != 0 {
		t.Fatal("resolved Skill did not use the common installation descriptor", err)
	}
	raw, err := json.Marshal(request.LocalEnvironment)
	if err != nil || bytes.Contains(raw, []byte("skill-private")) || bytes.Contains(raw, []byte("skill_reference")) {
		t.Fatal("public source identity reached Runtime", err)
	}
	environment.Configuration = []byte(`{"type":"openai_hosted","skills":[{"type":"skill_reference","skill_id":"skill-private","version":"latest"}]}`)
	if !LocalWorkspaceConfiguration(environment.Configuration) {
		t.Fatal("admission demanded installed metadata before the creation transaction")
	}
	if _, err := (&Dispatcher{}).configurePreparedEnvironment(t.Context(), session, environment, store.ExecutionDevice{EnvironmentID: environment.ID}, &proto.PromptRequestPayload{}); err == nil {
		t.Fatal("execution received an unresolved Skill selector")
	}
}

func TestLocalEnvironmentRequiresQualifiedProfileAndExactAuthority(t *testing.T) {
	for _, configuration := range []string{
		`{"type":"openai_hosted","network":{}}`,
		`{"type":"openai_hosted","network":{"access":"restricted"}}`,
		`{"type":"openai_hosted","network":{"access":"enabled","allowed_domains":["example.com"]}}`,
		`{"type":"openai_hosted","network":{"access":"disabled","allow":["example.com"]}}`,
		`{"type":"openai_hosted","network":{"access":"disabled"},"workspace_directory":"/override"}`,
	} {
		if _, err := parseEnvironmentPlacement([]byte(configuration)); err == nil {
			t.Fatalf("unqualified private profile accepted: %s", configuration)
		}
	}
	session := store.Session{ID: "session", TenantID: "tenant"}
	environment := store.Environment{ID: "environment", SessionID: session.ID, TenantID: session.TenantID, Configuration: []byte(`{"type":"openai_hosted","network":{"access":"disabled"}}`)}
	d := &Dispatcher{EnvironmentConnection: func(context.Context, store.Session, store.Environment) (EnvironmentConnection, error) {
		t.Fatal("local placement resolved a remote transport")
		return EnvironmentConnection{}, nil
	}}
	for _, scope := range []string{"", "other", environment.ID} {
		var req proto.PromptRequestPayload
		release, err := d.configurePreparedEnvironment(t.Context(), session, environment, store.ExecutionDevice{EnvironmentID: scope}, &req)
		if scope == environment.ID {
			if err != nil || release != nil || req.LocalEnvironment == nil || req.LocalEnvironment.ID != environment.ID || req.RemoteEnvironment != nil || req.WorkDir != "" {
				t.Fatal("local identity was not preserved", err)
			}
		} else if err == nil || req.LocalEnvironment != nil {
			t.Fatal("unscoped or foreign authority accepted")
		}
	}
}

func TestNetworkPolicySurvivesPreparedBinding(t *testing.T) {
	session := store.Session{ID: "session", TenantID: "tenant"}
	for _, network := range []string{`{"access":"disabled"}`, `{"access":"restricted","allowed_domains":["Example.com","api.example.com"]}`} {
		environment := store.Environment{ID: "environment", SessionID: session.ID, TenantID: session.TenantID,
			Configuration: []byte(`{"type":"openai_hosted","network":` + network + `}`)}
		placement, err := parseEnvironmentPlacement(environment.Configuration)
		if err != nil {
			t.Fatal(err)
		}
		var req proto.PromptRequestPayload
		_, err = (&Dispatcher{}).configurePreparedEnvironment(t.Context(), session, environment, store.ExecutionDevice{EnvironmentID: environment.ID}, &req)
		if err != nil || req.LocalEnvironment == nil || req.LocalEnvironment.NetworkAccess != placement.NetworkAccess || !slices.Equal(req.LocalEnvironment.AllowedDomains, placement.AllowedDomains) {
			t.Fatal("prepared binding lost policy", req.LocalEnvironment, err)
		}
	}
}

func TestSystemPackagesRemainRequiredInExecutionBinding(t *testing.T) {
	session := store.Session{ID: "session", TenantID: "tenant"}
	environment := store.Environment{ID: "environment", SessionID: session.ID, TenantID: session.TenantID,
		Configuration: []byte(`{"type":"openai_hosted","initialization":true,"packages":{"system":["jq"]}}`)}
	var req proto.PromptRequestPayload
	_, err := (&Dispatcher{}).configurePreparedEnvironment(t.Context(), session, environment,
		store.ExecutionDevice{EnvironmentID: environment.ID}, &req)
	if err != nil || req.LocalEnvironment == nil || !req.LocalEnvironment.ToolEnvironment || !req.LocalEnvironment.SystemPackages {
		t.Fatal("system initialization requirement was lost", err)
	}
	environment.Configuration = []byte(`{"type":"openai_hosted","packages":{"system":["jq"]}}`)
	if !LocalWorkspaceConfiguration(environment.Configuration) {
		t.Fatal("public admission requires a private execution receipt")
	}
	req = proto.PromptRequestPayload{}
	if _, err := (&Dispatcher{}).configurePreparedEnvironment(t.Context(), session, environment,
		store.ExecutionDevice{EnvironmentID: environment.ID}, &req); err == nil || req.LocalEnvironment != nil {
		t.Fatal("execution without the required initialization was admitted")
	}
}

func TestLocalNetworkDefaultsAndSupportedPolicies(t *testing.T) {
	for _, configuration := range []string{`{"type":"openai_hosted"}`, `{"type":"openai_hosted","network":null}`, `{"type":"openai_hosted","network":{"access":"enabled","allowed_domains":[]}}`} {
		got, err := parseEnvironmentPlacement([]byte(configuration))
		if err != nil || got.NetworkAccess != "enabled" {
			t.Fatal("enabled default lost", got, err)
		}
	}
	got, err := parseEnvironmentPlacement([]byte(`{"type":"openai_hosted","network":{"access":"disabled","allowed_domains":null}}`))
	if err != nil || got.NetworkAccess != "disabled" {
		t.Fatal("disabled policy lost", got, err)
	}
}
