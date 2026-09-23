package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestClaudeSessionConfigurationAdmission(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, input := range []string{`"Check the configured response."`, `[{"role":"user","content":[{"type":"input_text","text":"Check the configured response."}]}]`} {
			for _, test := range []struct {
				name, fields string
				accepted     bool
			}{
				{"defaults", `,"instructions":null`, true},
				{"schema", `,"text":{"format":{"type":"json_schema","schema":{"type":"object","properties":{"value":{"type":"string"}}}}}`, true},
				{"lossy schema", `,"text":{"format":{"type":"json_schema","schema":{"type":"object","const":9007199254740993}}}`, false},
				{"array schema", `,"text":{"format":{"type":"json_schema","schema":{"type":"array"}}}`, false},
				{"schema and MCP", `,"text":{"format":{"type":"json_schema","schema":{"type":"object"}}},"tools":[` + publicMCP + `]`, false},
				{"schema and subagents", `,"text":{"format":{"type":"json_schema","schema":{"type":"object"}}},"multi_agent":{"enabled":true}`, false},
				{"medium", `,"text":{"verbosity":"medium"}`, true},
				{"low", `,"text":{"verbosity":"low"}`, false},
				{"high", `,"text":{"verbosity":"high"}`, false},
				{"object function", `,"tools":[{"type":"function","name":"lookup","description":"Look up a value","parameters":{"type":"object","properties":{}}}]`, true},
				{"implicit root", `,"tools":[{"type":"function","name":"lookup","description":"Look up a value","parameters":{"properties":{}}}]`, false},
				{"union root", `,"tools":[{"type":"function","name":"lookup","description":"Look up a value","parameters":{"type":["object","null"]}}]`, false},
				{"MCP defaults", `,"tools":[` + publicMCP + `]`, true},
				{"MCP null", `,"tools":[` + strings.TrimSuffix(publicMCP, "}") + `,"allowed_tools":null}]`, true},
				{"MCP empty", `,"tools":[` + strings.TrimSuffix(publicMCP, "}") + `,"allowed_tools":[]}]`, true},
				{"MCP selected", `,"tools":[` + strings.TrimSuffix(publicMCP, "}") + `,"allowed_tools":["lookup.v1"]}]`, true},
				{"MCP required", `,"tools":[` + strings.TrimSuffix(publicMCP, "}") + `,"required":true}]`, true},
				{"MCP reserved label", `,"tools":[` + strings.Replace(publicMCP, `"records"`, `"functions"`, 1) + `]`, false},
				{"MCP invalid label", `,"tools":[` + strings.Replace(publicMCP, `"records"`, `"records.v1"`, 1) + `]`, false},
				{"MCP wildcard name", `,"tools":[` + strings.TrimSuffix(publicMCP, "}") + `,"allowed_tools":["*"]}]`, false},
				{"MCP empty fragment", `,"tools":[` + strings.Replace(publicMCP, `/tools"`, `/tools#"`, 1) + `]`, false},
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/input=%s", test.name, stream, input), func(t *testing.T) {
					digest := sha256.Sum256([]byte("test-api-key"))
					auth, err := NewAuthenticator([]APIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: hex.EncodeToString(digest[:]), TenantID: uuid.NewString()}})
					if err != nil {
						t.Fatal(err)
					}
					saved := &recordingStore{}
					handler, err := NewHandler(saved, auth, "claude_sdk", WithExecution(&inputRecorder{ResourceStore: saved}))
					if err != nil {
						t.Fatal(err)
					}
					body := fmt.Sprintf(`{"agent":{"model":"MiniMax-M3"%s},"environment":{"type":"none"},"stream":%t,"input":%s}`, test.fields, stream, input)
					request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(body))
					request.Header.Set("Authorization", "Bearer test-api-key")
					request.Header.Set("OpenAI-Beta", "agents=v1")
					request.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					want := http.StatusBadRequest
					if test.accepted {
						want = http.StatusCreated
						if stream {
							want = http.StatusServiceUnavailable
						}
					}
					if response.Code != want {
						t.Fatalf("status %d, expected %d: %s", response.Code, want, response.Body)
					}
					if want != http.StatusCreated && saved.tenant != "" {
						t.Fatal("rejected request persisted a Session")
					}
					if want == http.StatusCreated && saved.input.Engine != "claude_sdk" {
						t.Fatal("wrong engine persisted")
					}
				})
			}
		}
	}
}
