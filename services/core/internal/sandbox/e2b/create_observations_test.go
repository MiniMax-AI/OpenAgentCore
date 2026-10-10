package e2b

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func TestCreateObservationsRejectUnsafeDiagnostics(t *testing.T) {
	valid := `{"event":"e2b_create_stage","stage":"sandbox_create","duration_us":1250,"completed":true}`
	for _, bad := range []string{
		`private SDK secret`,
		strings.Replace(valid, `"sandbox_create"`, `"private SDK secret"`, 1),
		strings.Replace(valid, `1250`, `-1`, 1),
		strings.Replace(valid, `1250`, `1800000001`, 1),
		strings.Replace(valid, `1250`, `1.25`, 1),
		strings.Replace(valid, `1250`, `null`, 1),
		strings.Replace(valid, `true`, `null`, 1),
		strings.Replace(valid, `true`, `"private SDK secret"`, 1),
		strings.Replace(valid, `"completed":true`, `"credential":"private"`, 1),
		strings.TrimSuffix(valid, "}") + `,"credential":"private"}`,
		valid + `{}`,
		strings.Repeat(" ", 256) + valid,
	} {
		if got := parseCreateObservations([]byte(bad)); len(got) != 0 {
			t.Fatalf("unsafe diagnostic accepted: %q", bad)
		}
	}
	got := parseCreateObservations([]byte("private SDK diagnostic\n" + valid + "\n" + valid))
	if len(got) != 1 || *got[0].DurationUS != 1250 || !*got[0].Completed {
		t.Fatalf("valid timing lost or duplicated: %+v", got)
	}
}

func TestCreateObservationsRetainFailedStage(t *testing.T) {
	got := parseCreateObservations([]byte(`{"event":"e2b_create_stage","stage":"bootstrap_run","duration_us":0,"completed":false}`))
	if len(got) != 1 || *got[0].Completed {
		t.Fatal("failed stage discarded")
	}
}

func TestCreateObservationsAcceptBoundedGuestStages(t *testing.T) {
	for _, stage := range []string{"bootstrap_claim", "bootstrap_protection", "bootstrap_layout", "bootstrap_credentials", "bootstrap_spawn", "bootstrap_receipt"} {
		raw := `{"event":"e2b_create_stage","stage":"` + stage + `","duration_us":42,"completed":false}`
		got := parseCreateObservations([]byte(raw + "\n" + raw))
		if len(got) != 1 || got[0].Stage != stage || *got[0].Completed || *got[0].DurationUS != 42 {
			t.Fatal(got)
		}
	}
}

func TestProcessCreateObservationsReachLogger(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run(strconv.FormatBool(overflow), func(t *testing.T) {
			p, caller, ref := fixture(t)
			_, _ = p.Create(bounded(t), sandbox.Bootstrap{Reference: ref, SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "https://core.example/api/v1", Credential: "private-bootstrap", Harness: "codex", NetworkAccess: "enabled"})
			q := caller.requests[0]
			q.Deadline = time.Now().Add(time.Second)
			raw := `{"event":"e2b_create_stage","stage":"sandbox_create","duration_us":1250,"completed":true}` + "\nprivate SDK secret\n"
			if overflow {
				raw += strings.Repeat("x", MaxOutputBytes)
			}
			// Keep arbitrary diagnostics in a fixture file, never shell source.
			diagnostics := q.Config.StateDir + "/diagnostics"
			if err := os.WriteFile(diagnostics, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\ncat '" + diagnostics + "' >&2\nprintf '%s' '{\"Version\":" + strconv.Itoa(ProtocolVersion) + "}'\n"
			if err := os.WriteFile(q.Config.Binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			defer slog.SetDefault(previous)
			_, err := (&ProcessCaller{}).Call(context.Background(), q)
			if overflow {
				if err == nil || logs.Len() != 0 {
					t.Fatal("stderr overflow changed failure semantics or exported truncated diagnostics")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var record map[string]any
			if json.Unmarshal(logs.Bytes(), &record) != nil || record["allocation_id"] != ref.AllocationID || record["environment_id"] != ref.EnvironmentID || record["duration_ms"] != 1.25 || record["msg"] != "e2b create stage" {
				t.Fatalf("stage did not reach correlated logger: %s", logs.Bytes())
			}
			if strings.Contains(logs.String(), "private") {
				t.Fatal("SDK diagnostic leaked")
			}
		})
	}
}
