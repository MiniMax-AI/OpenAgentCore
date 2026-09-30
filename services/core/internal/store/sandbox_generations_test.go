package store

import (
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func TestE2BGenerationsRetainOwnershipAndUseCurrentCredential(t *testing.T) {
	s, w, view, input := webSpecificationFixture(t, "e2b")
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	owner := archiveAllocation(t, w, tenant, session, view.InstallationID)
	ctx := SandboxResetTestContext(t.Context())
	oldTemplate := input.Configuration.(*e2b.DeploymentConfiguration).Template
	input.Configuration.(*e2b.DeploymentConfiguration).Template = "next:" + uuid.NewString()
	input.Configuration.(*e2b.DeploymentConfiguration).APIURL, input.Configuration.(*e2b.DeploymentConfiguration).Domain = "https://sandbox.example.com", "sandbox.example.com"
	input.Resources.CPUs++
	changed, err := w.UpdateSandboxDeployment(ctx, view.InstallationID, SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: input, ExpectedGeneration: 1})
	if err != nil || changed.Generation != 2 || changed.OwnerEpoch != view.OwnerEpoch || changed.Rollout.PreviousGenerationSandboxes != 1 || changed.Rollout.State != "settled" {
		t.Fatal(changed, err)
	}
	assertSandboxSnapshotEquivalent(t, s.pool)
	ref := sandbox.Reference{TenantID: tenant, EnvironmentID: owner.EnvironmentID, AllocationID: owner.ID}
	retained, err := s.GetSandboxAllocationSetup(t.Context(), ref)
	if err != nil || retained.Generation != 1 || retained.Configuration.(*e2b.DeploymentConfiguration).Template != oldTemplate || retained.Configuration.(*e2b.DeploymentConfiguration).APIURL != "https://api.e2b.app" || retained.Specification.Resources.CPUs == input.Resources.CPUs {
		t.Fatal(retained, err)
	}
	input.Configuration.(*e2b.DeploymentConfiguration).APIKey = "replacement-secret"
	input.Configuration.(*e2b.DeploymentConfiguration).CredentialSupplied = true
	changed, err = w.UpdateSandboxDeployment(ctx, view.InstallationID, SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: input, ExpectedGeneration: 2})
	if err != nil || changed.Generation != 3 || changed.OwnerEpoch != view.OwnerEpoch {
		t.Fatal(changed, err)
	}
	retained, err = s.GetSandboxAllocationSetup(t.Context(), ref)
	if err != nil || retained.Generation != 1 || retained.Configuration.(*e2b.DeploymentConfiguration).APIKey != input.Configuration.(*e2b.DeploymentConfiguration).APIKey || retained.Configuration.(*e2b.DeploymentConfiguration).Template != oldTemplate || retained.Configuration.(*e2b.DeploymentConfiguration).APIURL != "https://api.e2b.app" {
		t.Fatal("old generation did not use committed key", err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE runtime_allocations SET deployment_generation=3 WHERE id=$1`, owner.ID); err == nil {
		t.Fatal("ownership generation was mutable")
	}
	if err = w.CollectSandboxGenerations(t.Context()); err != nil {
		t.Fatal(err)
	}
	generations, err := s.SandboxGenerationPage(t.Context(), -1)
	if err != nil || len(generations) != 1 || generations[0].Generation != 1 || generations[0].Configuration.(*e2b.DeploymentConfiguration).APIURL != "https://api.e2b.app" {
		t.Fatal(generations, err)
	}
	if _, err = w.RequestRuntimeCleanup(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err = w.SettleRuntimeCreation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err = w.ReleaseRuntimeAllocation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if err = w.CollectSandboxGenerations(t.Context()); err != nil {
		t.Fatal(err)
	}
	generations, err = s.SandboxGenerationPage(t.Context(), -1)
	if err != nil || len(generations) != 0 {
		t.Fatal(generations, err)
	}
	historical, err := s.GetRuntimeAllocation(t.Context(), tenant, owner.EnvironmentID)
	if err != nil || historical.DeploymentGeneration != 1 {
		t.Fatal("historical generation erased", err)
	}
}

func TestE2BRetainedCustomEndpointAfterOnlineSwitch(t *testing.T) {
	s, w, view, input := webSpecificationFixture(t, "e2b")
	ctx := SandboxResetTestContext(t.Context())
	input.Configuration.(*e2b.DeploymentConfiguration).APIURL, input.Configuration.(*e2b.DeploymentConfiguration).Domain = "https://sandbox.example.com", "sandbox.example.com"
	custom, err := w.UpdateSandboxDeployment(ctx, view.InstallationID, SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: input, ExpectedGeneration: view.Generation})
	if err != nil || custom.Generation != 2 {
		t.Fatal(custom, err)
	}
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	owner := archiveAllocation(t, w, tenant, session, view.InstallationID)
	input.Configuration.(*e2b.DeploymentConfiguration).APIURL, input.Configuration.(*e2b.DeploymentConfiguration).Domain = "", ""
	current, err := w.UpdateSandboxDeployment(ctx, view.InstallationID, SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: input, ExpectedGeneration: custom.Generation})
	if err != nil || current.Generation != 3 || e2bPublicConfiguration(t, current).APIURL != "https://api.e2b.app" {
		t.Fatal(current, err)
	}
	ref := sandbox.Reference{TenantID: tenant, EnvironmentID: owner.EnvironmentID, AllocationID: owner.ID}
	retained, err := s.GetSandboxAllocationSetup(t.Context(), ref)
	if err != nil || retained.Generation != 2 || retained.Configuration.(*e2b.DeploymentConfiguration).APIURL != "https://sandbox.example.com" || retained.Configuration.(*e2b.DeploymentConfiguration).Domain != "sandbox.example.com" {
		t.Fatal(retained, err)
	}
	generations, err := s.SandboxGenerationPage(t.Context(), -1)
	if err != nil || len(generations) != 1 || generations[0].Configuration.(*e2b.DeploymentConfiguration).APIURL != "https://sandbox.example.com" {
		t.Fatal(generations, err)
	}
}

func TestE2BChangeClassifierOmittedKeyAndExplicitSameKey(t *testing.T) {
	_, w, view, input := webSpecificationFixture(t, "e2b")
	key := input.Configuration.(*e2b.DeploymentConfiguration).APIKey
	input.Configuration.(*e2b.DeploymentConfiguration).APIKey = ""
	input.Resources = sandbox.Resources{}
	resolved, noOp, err := w.ClassifySandboxDeploymentChange(t.Context(), view.InstallationID, SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: input, ExpectedGeneration: 1})
	if err != nil || !noOp || resolved.Configuration.(*e2b.DeploymentConfiguration).APIKey != key || resolved.Resources.CPUs == 0 {
		t.Fatal(noOp, err)
	}
	input.Configuration.(*e2b.DeploymentConfiguration).APIKey = key
	input.Configuration.(*e2b.DeploymentConfiguration).CredentialSupplied = true
	_, noOp, err = w.ClassifySandboxDeploymentChange(t.Context(), view.InstallationID, SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: input, ExpectedGeneration: 1})
	if err != nil || noOp {
		t.Fatal("explicit same key skipped verification", err)
	}
	invalid := input
	invalid.Runtime = &sandbox.RuntimeRelease{}
	if _, _, err := w.ClassifySandboxDeploymentChange(t.Context(), view.InstallationID, SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: invalid, ExpectedGeneration: 1}); err == nil {
		t.Fatal("omitted resources erased forbidden Runtime input")
	}
	_, _, err = w.ClassifySandboxDeploymentChange(t.Context(), view.InstallationID, SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: input, ExpectedGeneration: 0})
	var stale *SandboxGenerationStaleError
	if !errors.As(err, &stale) {
		t.Fatal("stale did not precede no-op", err)
	}
}

func TestGenerationPinRetainsOfflineZeroResourceFallback(t *testing.T) {
	s, w, view, _ := webSpecificationFixture(t, "docker")
	node := specificationNode(t, s, view)
	if _, err := s.pool.Exec(t.Context(), `UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1`, node.NodeID); err != nil {
		t.Fatal(err)
	}
	// PR-N will make this transition through its generation protocol. This fixture
	// isolates the persistence invariant without enabling node online PUT in PR-G.
	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	q := s.queries.WithTx(tx)
	if _, err = q.LockRuntimeDeployment(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = q.RetainSandboxGeneration(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), `UPDATE runtime_deployment SET generation=2`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = w.CollectSandboxGenerations(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := s.SandboxGenerationPage(t.Context(), -1)
	if err != nil || len(rows) != 1 {
		t.Fatal("offline pin was collected", rows, err)
	}
	nodes, err := s.ListRuntimeNodes(t.Context())
	if err != nil || len(nodes) != 1 || nodes[0].Rollout.State != "unknown" || nodes[0].Rollout.ReadyGeneration == nil || *nodes[0].Rollout.ReadyGeneration != 1 {
		t.Fatal(nodes, err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE runtime_nodes SET removed_at=clock_timestamp(),ready_generation=NULL WHERE id=$1`, node.NodeID); err != nil {
		t.Fatal(err)
	}
	if err = w.CollectSandboxGenerations(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err = s.SandboxGenerationPage(t.Context(), -1)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}

func TestPendingPlacementGenerationSurvivesRepeatedUpdates(t *testing.T) {
	s, w, view, _ := webSpecificationFixture(t, "docker")
	node := specificationNode(t, s, view)
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	for generation := uint64(1); generation <= 2; generation++ {
		tx, err := s.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		q := s.queries.WithTx(tx)
		if _, err = q.LockRuntimeDeployment(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err = q.RetainSandboxGeneration(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(t.Context(), `UPDATE runtime_deployment SET generation=generation+1`); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	assertSandboxSnapshotEquivalent(t, s.pool)
	owner := archiveAllocation(t, w, tenant, session, view.InstallationID)
	if owner.DeploymentGeneration != 1 || owner.NodeID != node.NodeID {
		t.Fatal("pending allocation rebound to newest generation", owner)
	}
	if err := w.CollectSandboxGenerations(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := s.SandboxGenerationPage(t.Context(), -1)
	if err != nil || len(rows) != 1 || rows[0].Generation != 1 {
		t.Fatal(rows, err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE runtime_placements SET deployment_generation=3 WHERE environment_id=$1`, owner.EnvironmentID); err == nil {
		t.Fatal("placement generation changed")
	}
}

func TestGenerationMigrationBackfillsAndRejectsLossyDowngrade(t *testing.T) {
	db, migrations := runtimeNamesMigrationSchema(t)
	ctx := t.Context()
	if _, err := migrations.UpTo(ctx, 80); err != nil {
		t.Fatal(err)
	}
	installation, node := uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `UPDATE runtime_deployment SET installation_id=$1,backend_fingerprint=repeat('a',64),provider_kind='docker',mode='nodes',generation=1`, installation); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO runtime_nodes(id,installation_id,name,backend_fingerprint,credential_sha256,max_active,max_retained,deployment_generation) VALUES($1,$2,'old',repeat('a',64),repeat('b',64),1,1,1)`, node, installation); err != nil {
		t.Fatal(err)
	}
	session, tenant, environment, device, allocation := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash,configuration) VALUES($1,$2,'codex','old','old','{}')`, session, tenant)
	exec(`INSERT INTO environments(id,session_id) VALUES($1,$2)`, environment, session)
	exec(`INSERT INTO devices(id,tenant_id,name,credential_hash) VALUES($1,$2,'old',repeat('b',64))`, device, tenant)
	exec(`INSERT INTO runtime_placements(environment_id,node_id) VALUES($1,$2)`, environment, node)
	exec(`INSERT INTO runtime_allocations(id,environment_id,device_id,provider_key,node_id) VALUES($1,$2,$3,$4,$5)`, allocation, environment, device, installation, node)
	if _, err := migrations.UpTo(ctx, 81); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"runtime_allocations", "runtime_placements"} {
		var generation int64
		if err := db.QueryRowContext(ctx, `SELECT deployment_generation FROM `+table).Scan(&generation); err != nil || generation != 1 {
			t.Fatal("inexact migration backfill", table, generation, err)
		}
	}
	var pin int64
	if err := db.QueryRowContext(ctx, `SELECT ready_generation FROM runtime_nodes WHERE id=$1`, node).Scan(&pin); err != nil || pin != 1 {
		t.Fatal(pin, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE runtime_deployment SET generation=2`); err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.DownTo(ctx, 80); err == nil {
		t.Fatal("downgrade erased offline serving pin")
	}
	if _, err := db.ExecContext(ctx, `UPDATE runtime_nodes SET removed_at=clock_timestamp(),ready_generation=NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.DownTo(ctx, 80); err == nil {
		t.Fatal("downgrade discarded held allocation/placement after pin removal")
	}
	exec(`UPDATE runtime_allocations SET state='released',released_at=clock_timestamp(),create_settled=true`)
	if _, err := migrations.DownTo(ctx, 80); err == nil {
		t.Fatal("downgrade discarded unreleased placement")
	}
	exec(`UPDATE runtime_placements SET released_at=clock_timestamp()`)
	if _, err := migrations.DownTo(ctx, 80); err != nil {
		t.Fatal("settled downgrade failed", err)
	}
}

func TestGenerationDowngradeRefusesOldAllocation(t *testing.T) {
	s, w, view, input := webSpecificationFixture(t, "e2b")
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	owner := archiveAllocation(t, w, tenant, session, view.InstallationID)
	input.Configuration.(*e2b.DeploymentConfiguration).Template = "next:" + uuid.NewString()
	if _, err := w.UpdateSandboxDeployment(SandboxResetTestContext(t.Context()), view.InstallationID, SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: input, ExpectedGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(stdlib.GetConnector(*s.pool.Config().ConnConfig))
	defer db.Close()
	migrations, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"), goose.WithTableName("agents_api_schema_version"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrations.DownTo(t.Context(), 80); err == nil {
		t.Fatal("downgrade erased old owned allocation")
	}
	// Earlier down migrations can commit before the generation guard vetoes
	// downgrade. Restore the current schema before invoking current Store code.
	if _, err = migrations.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = w.RequestRuntimeCleanup(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err = w.SettleRuntimeCreation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err = w.ReleaseRuntimeAllocation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err = migrations.DownTo(t.Context(), 80); err != nil {
		t.Fatal("released history prevented safe downgrade", err)
	}
}

func TestGenerationUpdateSerializesWithAllocationAdmission(t *testing.T) {
	s, w, view, input := webSpecificationFixture(t, "e2b")
	for generation := uint64(1); generation <= 6; generation++ {
		tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
		input.Configuration.(*e2b.DeploymentConfiguration).Template = "next:" + uuid.NewString()
		start := make(chan struct{})
		changed := make(chan error, 1)
		go func() {
			<-start
			_, err := w.UpdateSandboxDeployment(SandboxResetTestContext(t.Context()), view.InstallationID, SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: input, ExpectedGeneration: generation})
			changed <- err
		}()
		close(start)
		owner := archiveAllocation(t, w, tenant, session, view.InstallationID)
		if err := <-changed; err != nil {
			t.Fatal(err)
		}
		if owner.DeploymentGeneration != generation && owner.DeploymentGeneration != generation+1 {
			t.Fatal("allocation bound unrelated generation", owner.DeploymentGeneration)
		}
		if err := w.CollectSandboxGenerations(t.Context()); err != nil {
			t.Fatal(err)
		}
		setup, err := s.GetSandboxAllocationSetup(t.Context(), sandbox.Reference{TenantID: tenant, EnvironmentID: owner.EnvironmentID, AllocationID: owner.ID})
		if err != nil || setup.Generation != owner.DeploymentGeneration {
			t.Fatal("committed allocation lost immutable routing", err)
		}
	}
}

func TestNodeRolloutSeparatesOfflinePinAndTargetReadiness(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		online, ready                  bool
		enrolled, pin, target          int64
		targetState, diagnostic, state string
	}{
		{"offline pin", false, true, 1, 1, 2, "ready", "", "unknown"},
		{"old serving", true, true, 1, 1, 2, "ready", "", "update_required"},
		{"current serving", true, true, 2, 2, 2, "ready", "", "ready"},
		{"current failed", true, false, 2, 2, 2, "failed", "kvm_unavailable", "failed"},
		{"current unknown", true, false, 2, 2, 2, "", "", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := nodeRollout(sqlc.ListRuntimeNodesRow{Online: tc.online, ProviderReady: tc.ready, DeploymentGeneration: tc.enrolled, ReadyGeneration: pgtype.Int8{Int64: tc.pin, Valid: true}, TargetGeneration: tc.target, ProtocolVersion: 1, TargetState: tc.targetState, TargetDiagnostic: tc.diagnostic})
			if got.State != tc.state || got.Diagnostic != tc.diagnostic || got.ReadyGeneration == nil || *got.ReadyGeneration != uint64(tc.pin) {
				t.Fatal(got)
			}
		})
	}
}

func TestSandboxSnapshotRolloutEquivalence(t *testing.T) {
	s, w, view, input := webSpecificationFixture(t, "docker")
	node := specificationNode(t, s, view)
	for _, state := range []string{"ready", "preparing", "failed", "unconfirmed", "offline", "update_required"} {
		t.Run(state, func(t *testing.T) {
			connection := onlineManagerNode(t, s, node.NodeID)
			want := SandboxRolloutNodes{}
			wantState := "settled"
			switch state {
			case "ready":
				want.Ready = 1
			case "preparing":
				generationHeartbeat(t, s, node, connection, view, "preparing")
				want.Preparing = 1
				wantState = "preparing"
			case "failed":
				generationHeartbeat(t, s, node, connection, view, "failed")
				want.Failed = 1
			case "unconfirmed":
				connection = uuid.NewString()
				if err := s.ConnectRuntimeNode(t.Context(), node.NodeID, connection, view.OwnerEpoch); err != nil {
					t.Fatal(err)
				}
				if err := s.HeartbeatRuntimeNodeGenerations(t.Context(), node.NodeID, connection, view.OwnerEpoch, RuntimeNodeHealth{}, nil); err != nil {
					t.Fatal(err)
				}
				want.Unknown = 1
			case "offline":
				runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_nodes SET last_seen_at=clock_timestamp()-interval '46 seconds',health='{"diagnostic":"provider_unavailable"}' WHERE id=$1`, node.NodeID)
				want.Unknown = 1
			case "update_required":
				changeNodeTarget(t, w, view, input)
				want.UpdateRequired = 1
			}
			assertSandboxSnapshotEquivalent(t, s.pool)
			got, err := s.GetRuntimeDeployment(t.Context())
			if err != nil || got.Rollout.Nodes == nil || *got.Rollout.Nodes != want || got.Rollout.State != wantState {
				t.Fatal("rollout classification changed", got.Rollout, err)
			}
		})
	}
}
