package codex

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
)

func preparationLogStages(t *testing.T, buf *bytes.Buffer, failed, outcome string) []string {
	t.Helper()
	var stages []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["msg"] != "codex preparation failed" {
			continue
		}
		for key := range entry {
			switch key {
			case "time", "level", "msg", "session_id", "phase", "duration_ms", "context_outcome":
			default:
				t.Fatalf("unexpected preparation log field %q", key)
			}
		}
		stage, ok := entry["phase"].(string)
		if !ok || stage != failed || entry["duration_ms"].(float64) < 0 {
			t.Fatalf("incorrect preparation outcome: %v", entry)
		}
		want := "active"
		if stage == failed {
			want = outcome
		}
		if entry["context_outcome"] != want {
			t.Fatalf("incorrect context outcome: %v", entry)
		}
		stages = append(stages, stage)
	}
	return stages
}

func TestPreparationLogsLocateNativeFailureWithoutPayloads(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "mcp-rejected"}[reject], func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			req.Assignment.SessionID = "00000000-0000-4000-8000-000000000001"
			req.Skills = []agentcapabilities.InstalledSkill{{InstallationRoot: root, RelativeRoot: "PRIVATE_SKILL_PATH"}}
			var buf bytes.Buffer
			cfg.logger = slog.New(slog.NewJSONHandler(&buf, nil))
			if reject {
				if err := os.WriteFile(os.Getenv("OAC_TEST_PREPARATION_MCP_CONFIG"), []byte(`{"config":{"mcp_servers":{"PRIVATE_SECRET":{"url":"https://private.invalid/private-token"}}}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			e, err := testExecutor(t, "complete", req, cfg)
			if (err != nil) != reject || (e == nil) != reject {
				t.Fatal("preparation result changed", err)
			}
			if e != nil {
				if err := e.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			assertPreparationOnly(t, root)
			if len(preparedCatalogs(t, root)) != 0 {
				t.Fatal("preparation cleanup leaked catalog")
			}
			failed := ""
			var want []string
			if reject {
				failed = "mcp_configuration"
				if !strings.Contains(buf.String(), `"session_id":"`+req.Assignment.SessionID+`"`) {
					t.Fatal("missing assignment Session correlation")
				}
				want = []string{"mcp_configuration"}
			}
			if got := preparationLogStages(t, &buf, failed, "active"); !reflect.DeepEqual(got, want) {
				t.Fatalf("stages %v, want %v", got, want)
			}
			for _, private := range []string{"PRIVATE_SECRET", "PRIVATE_SKILL_PATH", "private-token", filepath.Join(root, "home"), "fixture-key"} {
				if strings.Contains(buf.String(), private) {
					t.Fatal("private preparation data in log")
				}
			}
		})
	}
}
