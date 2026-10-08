package runtimegateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
)

type bindingStore struct {
	LinkStore
	assignment runtimedevice.LinkAssignment
	signed     int
}

func (s *bindingStore) GetLinkAssignment(context.Context, string) (runtimedevice.LinkAssignment, bool, error) {
	return s.assignment, true, nil
}

func (s *bindingStore) SignAttachGrant(context.Context, string) (string, error) {
	s.signed++
	return strings.Repeat("a", 64), nil
}

func TestBindLinkResolvesWorkspaceBeforeSigning(t *testing.T) {
	ref := proto.AssignmentRef{SessionID: "session", AssignmentID: "4fe54117-6229-41fa-ac03-104754dc3957", Epoch: 1}
	for _, tc := range []struct {
		name, configuration, environment, workspace string
		rejected                                    bool
	}{
		{"hosted", `{"type":"openai_hosted"}`, "environment", "/workspace", false},
		{"self hosted", `{"type":"self_hosted","workspace_directory":"/home/user/project"}`, "environment", "/home/user/project", false},
		{"none", `{"type":"none"}`, "", "", false},
		{"relative workspace", `{"type":"self_hosted","workspace_directory":"relative"}`, "environment", "", true},
		{"unrecognized configuration", `{"type":"self_hosted","workspace_directory":"/project","extra":true}`, "environment", "", true},
		{"missing environment", `{"type":"none"}`, "environment", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &bindingStore{assignment: runtimedevice.LinkAssignment{SessionID: ref.SessionID, RuntimeID: "host", Epoch: ref.Epoch, Bound: true, AgentHost: true,
				Resource: sandboxbootstrap.Resource{EnvironmentID: "environment", Kind: "enrollment", ID: "resource", Generation: 1}, EnvironmentConfiguration: json.RawMessage(tc.configuration)}}
			payload, err := NewLinkAuthority(store).bindLink(t.Context(), "host", ref, tc.environment)
			if tc.rejected {
				if err == nil || store.signed != 0 {
					t.Fatal("invalid workspace gained an attach grant", err, store.signed)
				}
				return
			}
			if err != nil || payload.EnvironmentID != tc.environment || payload.WorkspaceDirectory != tc.workspace {
				t.Fatal("bind lost its workspace", payload, err)
			}
			if tc.environment == "" {
				if payload.Resource != nil || len(payload.AttachGrant) != 0 || store.signed != 0 {
					t.Fatal("none gained Link authority", payload)
				}
			} else if payload.Resource == nil || len(payload.AttachGrant) == 0 || store.signed != 1 {
				t.Fatal("workspace bind lost Link authority", payload)
			}
		})
	}
}
