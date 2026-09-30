package execution

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Public Turn failure can describe lost orchestration, independently of native
// failure. Late observations must not replace its unknown-effect reason or
// manufacture a native terminal result.
func TestRuntimeProtocolUnknownEffectRetainsObservationFailure(t *testing.T) {
	for _, reason := range []string{"delivery_unknown", "event_stream_incomplete"} {
		t.Run(reason, func(t *testing.T) {
			result := Result{ErrorCode: reason, AppliedThrough: 1}
			writer := &recoveringWriter{}
			j := journal{store: writer, next: 1}
			events := make(chan proto.Envelope, 1)
			usage, err := proto.NewEnvelope(proto.TypeUsage, "run", proto.Usage{InputTokens: 7})
			if err != nil {
				t.Fatal(err)
			}
			events <- usage
			close(events)
			if err := j.drain(events, &result); err != nil {
				t.Fatal(err)
			}
			// A cancellation waiter deadline is not an application receipt.
			if err := recordCancellation(context.Background(), &j, cancellationResult{err: context.DeadlineExceeded}, &result); err != nil {
				t.Fatal(err)
			}
			if err := j.flush(context.Background()); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var persisted Result
			if err := json.Unmarshal(raw, &persisted); err != nil {
				t.Fatal(err)
			}
			if persisted.ErrorCode != reason || persisted.EngineErrorCode != "" || persisted.EngineHTTPStatus != nil ||
				persisted.Done.SourceCompletedAtMS != nil || persisted.Done.Usage.InputTokens != 7 || persisted.AppliedThrough != 1 {
				t.Fatalf("unknown result changed into native terminal evidence: %+v", persisted)
			}
			if len(writer.events) != 1 || writer.events[0].Kind != proto.TypeUsage {
				t.Fatalf("unknown delivery fabricated completion or cancellation: %+v", writer.events)
			}
		})
	}
}
