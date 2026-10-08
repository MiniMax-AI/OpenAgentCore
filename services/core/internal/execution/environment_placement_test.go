package execution

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestSkillReferenceIdentityStopsAtCoreBoundary(t *testing.T) {
	session := sessions.Session{ID: "session", TenantID: "tenant"}
	environment := sessions.Environment{ID: "environment", SessionID: session.ID, TenantID: session.TenantID,
		Configuration: []byte(`{"type":"openai_hosted","initialization":true,"skills":[{"type":"skill_reference","skill_id":"skill-private","version":"1","name":"proof","description":"A proof."}]}`)}
	var request proto.PromptRequestPayload
	err := (&Dispatcher{}).configurePreparedEnvironment(session, environment, sessions.ExecutionDevice{SessionEnvironmentID: environment.ID}, &request)
	if err != nil || request.LocalEnvironment == nil {
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
	if err := (&Dispatcher{}).configurePreparedEnvironment(session, environment, sessions.ExecutionDevice{SessionEnvironmentID: environment.ID}, &proto.PromptRequestPayload{}); err == nil {
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
	session := sessions.Session{ID: "session", TenantID: "tenant"}
	environment := sessions.Environment{ID: "environment", SessionID: session.ID, TenantID: session.TenantID, Configuration: []byte(`{"type":"openai_hosted","network":{"access":"disabled"}}`)}
	d := &Dispatcher{}
	for _, scope := range []string{"", "other", environment.ID} {
		var req proto.PromptRequestPayload
		err := d.configurePreparedEnvironment(session, environment, sessions.ExecutionDevice{SessionEnvironmentID: scope}, &req)
		if scope == environment.ID {
			if err != nil || req.LocalEnvironment == nil || req.LocalEnvironment.ID != environment.ID {
				t.Fatal("local identity was not preserved", err)
			}
		} else if err == nil || req.LocalEnvironment != nil {
			t.Fatal("unscoped or foreign authority accepted")
		}
	}
}

func TestToolEnvironmentRemainsInExecutionBinding(t *testing.T) {
	session := sessions.Session{ID: "session", TenantID: "tenant"}
	environment := sessions.Environment{ID: "environment", SessionID: session.ID, TenantID: session.TenantID, Configuration: []byte(`{"type":"openai_hosted","initialization":true,"packages":{"npm":["is-number"]}}`)}
	var req proto.PromptRequestPayload
	err := (&Dispatcher{}).configurePreparedEnvironment(session, environment, sessions.ExecutionDevice{SessionEnvironmentID: environment.ID}, &req)
	if err != nil || req.LocalEnvironment == nil || !req.LocalEnvironment.ToolEnvironment {
		t.Fatal("tool initialization requirement lost", err)
	}
}
