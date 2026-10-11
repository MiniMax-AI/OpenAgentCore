package execution

import (
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Exercise the SQL projections with the same serialized envelope Core persists.
// Adapter-private data deliberately contains legacy-looking keys: queries must
// read only the shared retained envelope, never the opaque payload.
func TestRuntimeRetainedStateFeedsCheckpointDemandAndNodeCounts(t *testing.T) {
	f := newWorkspaceSettlementFixture(t, &retentionProvider{})
	r, session := f.lifecycle, f.session
	owner, err := r.provision(t.Context(), session.TenantID, session.Environment.ID, r.config.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	compatibility := sandbox.CheckpointCompatibility{ArtifactDomain: "fixture-store", ExecutionClass: "fixture-runtime"}
	source := sandbox.Compute{Generation: 1, Name: "source", ID: "native-source"}
	retained := sandbox.RetainedState{Reference: "fixture/retained", ID: "native-retained", Data: `{"snapshot":{"Compatibility":{"artifact_domain":"private","execution_class":"private"}}}`, OperationID: "suspend-operation", SourceGeneration: source.Generation, SourceName: source.Name, SourceID: source.ID, Compatibility: compatibility}
	for _, tc := range []struct {
		name                                             string
		wake, expired, released, noCompatibility, legacy bool
		demand, count                                    int
	}{
		{name: "waiting", wake: true, demand: 1, count: 1},
		{name: "idle", count: 1},
		{name: "expired", wake: true, expired: true, count: 1},
		{name: "released", wake: true, released: true},
		{name: "no_compatibility", wake: true, noCompatibility: true, count: 1},
		{name: "legacy_shape", wake: true, legacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := runtimeCompute{Version: sandbox.SuspensionStateVersion, Current: source, Retained: &retained, SuspendID: retained.OperationID}
			copied := retained
			if tc.noCompatibility {
				copied.Compatibility = sandbox.CheckpointCompatibility{}
				state.Retained = &copied
			}
			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if tc.legacy {
				var envelope map[string]json.RawMessage
				if err := json.Unmarshal(raw, &envelope); err != nil {
					t.Fatal(err)
				}
				envelope["snapshot"] = envelope["retained"]
				delete(envelope, "retained")
				raw, err = json.Marshal(envelope)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET state=CASE WHEN $3 THEN 'released' ELSE 'running' END,released_at=CASE WHEN $3 THEN clock_timestamp() END,compute_phase='suspended',compute_state=$2,compute_retained_until=clock_timestamp()+CASE WHEN $4 THEN interval '-1 hour' ELSE interval '1 hour' END,compute_wake_requested=$5 WHERE id=$1`, owner.ID, raw, tc.released, tc.expired, tc.wake); err != nil {
				t.Fatal(err)
			}
			q := sqlc.New(f.pool)
			demands, err := q.ListWaitingCheckpointRestores(t.Context())
			if err != nil || len(demands) != tc.demand {
				t.Fatalf("checkpoint demand=%d want=%d: %v", len(demands), tc.demand, err)
			}
			if len(demands) == 1 {
				var got sandbox.CheckpointCompatibility
				if err := json.Unmarshal(demands[0].Checkpoint, &got); err != nil || got != compatibility {
					t.Fatalf("shared compatibility lost: %+v %v", got, err)
				}
			}
			nodes, err := q.ListRuntimeNodes(t.Context(), pgtype.UUID{Bytes: uuid.MustParse(owner.NodeID), Valid: true})
			if err != nil || len(nodes) != 1 {
				t.Fatalf("node projection: %d %v", len(nodes), err)
			}
			if nodes[0].Snapshots != int64(tc.count) {
				t.Fatalf("retained node count=%d want=%d", nodes[0].Snapshots, tc.count)
			}
		})
	}
}
