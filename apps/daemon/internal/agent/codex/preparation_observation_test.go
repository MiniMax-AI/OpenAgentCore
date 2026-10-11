package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

func TestPreparationObservationIsCorrelatedAndSecretSafe(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(obslog.NewContextHandler(slog.NewJSONHandler(&output, nil))))
	defer slog.SetDefault(previous)
	carrier, err := obslog.ParseTraceparent("00-12345678901234567890123456789012-1234567890123456-01")
	if err != nil {
		t.Fatal(err)
	}
	ctx := obslog.WithTrace(t.Context(), carrier)
	for _, err := range []error{nil, errors.New("fixture-secret-command-output")} {
		observePreparationStage(ctx, "model_catalog", time.Now(), err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatal(output.String())
	}
	for i, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["trace_id"] != "12345678901234567890123456789012" || entry["stage"] != "model_catalog" || entry["success"] != (i == 0) {
			t.Fatal(entry)
		}
		if entry["duration_ms"].(float64) < 0 {
			t.Fatal(entry)
		}
		for key := range entry {
			switch key {
			case "time", "level", "msg", "trace_id", "span_id", "stage", "duration_ms", "success":
			default:
				t.Fatalf("unexpected field %s", key)
			}
		}
	}
	if strings.Contains(output.String(), "fixture-secret") {
		t.Fatal("native error leaked")
	}
}
