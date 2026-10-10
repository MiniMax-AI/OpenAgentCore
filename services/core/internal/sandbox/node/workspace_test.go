package node

import (
	"encoding/json"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"testing"
)

func TestWorkspaceWireBindsConfigurationAndEnvironment(t *testing.T) {
	ref := sandbox.Reference{TenantID: "11111111-1111-4111-8111-111111111111", EnvironmentID: "22222222-2222-4222-8222-222222222222", AllocationID: "33333333-3333-4333-8333-333333333333"}
	config := workspacefs.Configuration{ID: "44444444-4444-4444-8444-444444444444", Adapter: "test", Parameters: json.RawMessage(`{}`)}
	binding := workspacefs.Binding{Configuration: config, Attachment: workspacefs.Attachment{Reference: workspacefs.Reference{TenantID: ref.TenantID, EnvironmentID: ref.EnvironmentID, ObjectID: "55555555-5555-4555-8555-555555555555"}, ConfigurationID: config.ID, Kind: workspacefs.AttachmentHostDirectory, Native: json.RawMessage(`{}`)}}
	for _, fault := range []string{"match", "environment", "tenant", "configuration", "object"} {
		t.Run(fault, func(t *testing.T) {
			b := binding
			switch fault {
			case "environment":
				b.Attachment.Reference.EnvironmentID = ref.TenantID
			case "tenant":
				b.Attachment.Reference.TenantID = ref.EnvironmentID
			case "configuration":
				b.Attachment.ConfigurationID = ref.AllocationID
			case "object":
				b.Attachment.Reference.ObjectID = "invalid"
			}
			q := request{ID: ref.AllocationID, Reference: ref, Operation: "create", TimeoutMillis: 1000, Bootstrap: &sandbox.Bootstrap{Reference: ref, Workspace: &b}}
			if (q.validate() == nil) != (fault == "match") {
				t.Fatal("invalid create binding forwarding", fault)
			}
			q.Operation = "resume"
			q.Bootstrap = nil
			q.Resume = &sandbox.ResumeRequest{Reference: ref, Workspace: &b}
			if (q.validate() == nil) != (fault == "match") {
				t.Fatal("invalid restore binding forwarding", fault)
			}
		})
	}
}

func TestWorkspaceUnsupportedResponseSurvivesStrictFrame(t *testing.T) {
	out := response{ID: "11111111-1111-4111-8111-111111111111", ConnectionID: "22222222-2222-4222-8222-222222222222", ErrorCode: "unsupported", Unsupported: &providercontract.UnsupportedError{Operation: "Create", Reason: "external_workspace_unsupported"}}
	raw, err := json.Marshal(frame{Version: ProtocolVersion, Type: "response", Response: &out})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeFrame(raw)
	if err != nil {
		t.Fatal("explicit workspace rejection broke transport", err)
	}
	if reason, ok := providercontract.UnsupportedReason(responseError(*decoded.Response), "Create"); !ok || reason != "external_workspace_unsupported" {
		t.Fatal("lost typed rejection")
	}
}
