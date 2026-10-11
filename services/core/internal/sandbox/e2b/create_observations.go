package e2b

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

type createObservation struct {
	Event      string `json:"event"`
	Stage      string `json:"stage"`
	DurationUS *int64 `json:"duration_us"`
	Completed  *bool  `json:"completed"`
}

// Helper stderr is untrusted. Only fixed stage names and bounded numeric fields
// reach the logger; arbitrary SDK diagnostics are never forwarded.
func parseCreateObservations(raw []byte) []createObservation {
	var out []createObservation
	seen := make(map[string]bool)
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(line) > 256 {
			continue
		}
		var value createObservation
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&value) != nil || value.Event != "e2b_create_stage" || value.DurationUS == nil || value.Completed == nil {
			continue
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF || *value.DurationUS < 0 || *value.DurationUS > 1_800_000_000 || seen[value.Stage] {
			continue
		}
		switch value.Stage {
		case "sandbox_create", "ownership_check", "template_check", "bootstrap_write", "bootstrap_run", "ready_inspect",
			"bootstrap_stream_open", "bootstrap_stream_completion",
			"bootstrap_claim", "bootstrap_protection", "bootstrap_layout", "bootstrap_credentials", "bootstrap_spawn", "bootstrap_receipt":
			seen[value.Stage] = true
			out = append(out, value)
		}
	}
	return out
}

func observeHelperCreate(ctx context.Context, q Request, raw []byte) {
	if q.Operation != "create" {
		return
	}
	for _, value := range parseCreateObservations(raw) {
		obslog.Ctx(ctx).Info("e2b create stage", "stage", value.Stage,
			"duration_ms", float64(*value.DurationUS)/1000, "completed", *value.Completed,
			"environment_id", q.Reference.EnvironmentID, "allocation_id", q.Reference.AllocationID)
	}
}
