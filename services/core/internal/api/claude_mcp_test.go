package api

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

type claudeCredentialStore struct {
	binding store.MCPCredentialBinding
	calls   int
}

func (s *claudeCredentialStore) ResolveMCPCredentials(_ context.Context, _ string, _ []string, _ []store.MCPCredentialRequest) ([]store.MCPCredentialBinding, error) {
	s.calls++
	return []store.MCPCredentialBinding{s.binding}, nil
}

func TestClaudeMCPAdmitsResolvedCredentials(t *testing.T) {
	for _, selection := range []string{"implicit", "explicit", "unmatched"} {
		t.Run(selection, func(t *testing.T) {
			vault, credential := uuid.NewString(), uuid.NewString()
			s := &claudeCredentialStore{binding: store.MCPCredentialBinding{ServerLabel: "records", ServerURL: "https://mcp.example.test/tools", VaultID: vault, CredentialID: credential, AuthType: "static_bearer"}}
			tool := publicMCP
			if selection == "explicit" {
				tool = strings.TrimSuffix(tool, "}") + `,"credential_id":"` + credential + `"}`
			}
			if selection == "unmatched" {
				s.binding.VaultID, s.binding.CredentialID, s.binding.AuthType = "", "", ""
			}
			h, recording, _ := testHandler(t, func(d *Dependencies, f *testFakes) {
				d.Engine = "claude_sdk"
				admitSessions(d, f)
				f.vaults.resolveMCPCredentials = s.ResolveMCPCredentials
			})
			body := fmt.Sprintf(`{"agent":{"model":"model","tools":[%s]},"environment":{"type":"none"},"vault_ids":[%q],"input":"Use the configured records server."}`, tool, vault)
			response := credentialRequest(h, "POST", "/v1/agents/sessions", body)
			if response.Code != 201 || s.calls != 1 || recording.tenant == "" {
				t.Fatal("credential selection or admission failed", response.Code, response.Body, s.calls)
			}
		})
	}
}
