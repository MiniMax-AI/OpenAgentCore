package mcode

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestPreparationLogsStagesWithoutNativePayloads(t *testing.T) {
	for _, tc := range []struct {
		name, scenario, phase, outcome string
		resume, success                bool
	}{
		{"initialize", "prepare-reject-initialize", "initialize", "active", false, false},
		{"new", "prepare-reject-session/new", "session/new", "active", false, false},
		{"load", "prepare-reject-session/load", "session/load", "active", true, false},
		{"model-selection", "unknown-model", "model_selection", "active", false, false},
		{"model-configuration", "prepare-reject-session/set_config_option", "model_configuration", "active", false, false},
		{"success", "complete", "model_configuration", "active", false, true},
		{"cancelled", "hang", "initialize", "cancelled", false, false},
		{"deadline", "hang", "initialize", "deadline_exceeded", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			defer slog.SetDefault(previous)
			req := testRequest(t)
			req.SystemPrompt = "PRIVATE_INSTRUCTIONS"
			req.ModelProvider.APIKey = "PRIVATE_MODEL_KEY"
			if tc.resume {
				req.AgentSessionID = "PRIVATE_NATIVE_SESSION"
			}
			ctx := t.Context()
			if tc.outcome == "deadline_exceeded" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
			} else if tc.outcome == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				timer := time.AfterFunc(200*time.Millisecond, cancel)
				defer timer.Stop()
			}
			e, err := prepareExecutor(t, ctx, helperInstall(t, tc.scenario, ""), req)
			if (err == nil) != tc.success || (e != nil) != tc.success {
				t.Fatalf("preparation result changed: owner=%v err=%v", e != nil, err)
			}
			if strings.HasPrefix(tc.scenario, "prepare-reject-") && !strings.Contains(err.Error(), "PRIVATE_NATIVE_ERROR private-token") {
				t.Fatal("native error was changed")
			}
			if e != nil {
				if err := e.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			var entry map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &entry); err != nil {
				t.Fatalf("expected exactly one preparation log: %v", err)
			}
			if entry["msg"] != "mcode preparation" || entry["phase"] != tc.phase || entry["success"] != tc.success || entry["context_outcome"] != tc.outcome {
				t.Fatalf("incorrect preparation outcome: %v", entry)
			}
			if duration, ok := entry["duration_ms"].(float64); !ok || duration < 0 {
				t.Fatalf("invalid preparation duration: %v", entry)
			}
			for key := range entry {
				switch key {
				case "time", "level", "msg", "phase", "duration_ms", "success", "context_outcome":
				default:
					t.Fatalf("unexpected diagnostic field %q", key)
				}
			}
			for _, secret := range []string{"PRIVATE_", "private-token", req.ModelProvider.BaseURL, "m:custom_provider", "fixture"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("private preparation data in log")
				}
			}
		})
	}
}
