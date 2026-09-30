package execution

import (
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestMCPFrozenCredentialAdmission(t *testing.T) {
	vault, credential := uuid.NewString(), uuid.NewString()
	for _, mode := range []string{"implicit", "explicit", "oauth implicit", "oauth explicit", "anonymous", "missing", "unattached", "wrong URL", "wrong auth", "changed selection", "HTTP", "remote", "self-hosted explicit", "self-hosted implicit", "self-hosted anonymous"} {
		t.Run(mode, func(t *testing.T) {
			tool := v1.MCPTool{Type: "mcp", ServerLabel: "tools", ConnectionOrigin: "service", Transport: v1.MCPHTTPTransport{Type: "http", ServerURL: "https://mcp.example/tools"}}
			binding := store.MCPCredentialBinding{ServerLabel: "tools", ServerURL: tool.Transport.ServerURL, VaultID: vault, CredentialID: credential, AuthType: "static_bearer"}
			snapshot := Snapshot{Agent: v1.Agent{Model: "model"}, Environment: &v1.Environment{Type: "none"}, VaultIDs: []string{vault}}
			switch mode {
			case "explicit", "oauth explicit", "missing", "changed selection", "self-hosted explicit":
				tool.CredentialID = &credential
			case "anonymous", "self-hosted anonymous":
				binding.VaultID, binding.CredentialID, binding.AuthType = "", "", ""
			case "unattached":
				snapshot.VaultIDs = nil
			case "wrong URL":
				binding.ServerURL += "/other"
			case "wrong auth":
				binding.AuthType = "other"
			case "HTTP":
				tool.Transport.ServerURL, binding.ServerURL = "http://mcp.example/tools", "http://mcp.example/tools"
			case "remote":
				snapshot.Daemon = &DaemonConfig{WorkDir: "/tmp"}
			}
			if strings.HasPrefix(mode, "oauth ") {
				binding.AuthType = "mcp_oauth"
			}
			if strings.HasPrefix(mode, "self-hosted") {
				snapshot.Environment = &v1.Environment{Type: "self_hosted", WorkspaceDirectory: "/workspace"}
			}
			if mode == "changed selection" {
				binding.CredentialID = uuid.NewString()
			}
			snapshot.MCPCredentials = []store.MCPCredentialBinding{binding}
			if mode == "missing" {
				snapshot.MCPCredentials = nil
			}
			rawTool, _ := json.Marshal(tool)
			snapshot.Agent.Tools = []json.RawMessage{rawTool}
			raw, _ := json.Marshal(snapshot)
			valid := mode == "implicit" || mode == "explicit" || mode == "anonymous" || strings.HasPrefix(mode, "oauth ")
			for _, engine := range []string{"codex", "claude_sdk"} {
				supported := valid && (engine == "codex" || !strings.HasPrefix(mode, "self-hosted"))
				if err := (Policy{}).ValidateSessionConfiguration(engine, raw); (err == nil) != supported {
					t.Fatal("frozen binding profile decision differs", engine, err)
				}
			}
		})
	}
}
