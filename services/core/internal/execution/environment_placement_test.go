package execution

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestSkillReferenceIdentityStopsAtCoreBoundary(t *testing.T) {
	session := store.Session{ID: "session", TenantID: "tenant"}
	environment := store.Environment{ID: "environment", SessionID: session.ID, TenantID: session.TenantID,
		Configuration: []byte(`{"type":"openai_hosted","initialization":true,"skills":[{"type":"skill_reference","skill_id":"skill-private","version":"1","name":"proof","description":"A proof."}]}`)}
	var request proto.PromptRequestPayload
	err := (&Dispatcher{}).configurePreparedEnvironment(session, environment, store.ExecutionDevice{EnvironmentID: environment.ID}, &request)
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
	if err := (&Dispatcher{}).configurePreparedEnvironment(session, environment, store.ExecutionDevice{EnvironmentID: environment.ID}, &proto.PromptRequestPayload{}); err == nil {
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
	d := &Dispatcher{}
	for _, scope := range []string{"", "other", environment.ID} {
		var req proto.PromptRequestPayload
		err := d.configurePreparedEnvironment(session, environment, store.ExecutionDevice{EnvironmentID: scope}, &req)
		if scope == environment.ID {
			if err != nil || req.LocalEnvironment == nil || req.LocalEnvironment.ID != environment.ID {
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
		err = (&Dispatcher{}).configurePreparedEnvironment(session, environment, store.ExecutionDevice{EnvironmentID: environment.ID}, &req)
		if err != nil || req.LocalEnvironment == nil || req.LocalEnvironment.NetworkAccess != placement.NetworkAccess || !slices.Equal(req.LocalEnvironment.AllowedDomains, placement.AllowedDomains) {
			t.Fatal("prepared binding lost policy", req.LocalEnvironment, err)
		}
	}
}

func TestToolEnvironmentRemainsInExecutionBinding(t *testing.T) {
	session := store.Session{ID: "session", TenantID: "tenant"}
	environment := store.Environment{ID: "environment", SessionID: session.ID, TenantID: session.TenantID, Configuration: []byte(`{"type":"openai_hosted","initialization":true,"packages":{"npm":["is-number"]}}`)}
	var req proto.PromptRequestPayload
	err := (&Dispatcher{}).configurePreparedEnvironment(session, environment, store.ExecutionDevice{EnvironmentID: environment.ID}, &req)
	if err != nil || req.LocalEnvironment == nil || !req.LocalEnvironment.ToolEnvironment {
		t.Fatal("tool initialization requirement lost", err)
	}
}
