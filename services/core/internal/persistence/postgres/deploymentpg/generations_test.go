package deploymentpg_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
)

// generationsState is the stored deployment, its retained generations and the
// administrator audit, as one comparable value.
func generationsState(t *testing.T, f fixture) string {
	t.Helper()
	var state string
	if err := f.pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
		'deployment', (SELECT to_jsonb(d) FROM runtime_deployment d),
		'generations', (SELECT coalesce(jsonb_agg(to_jsonb(g) ORDER BY g.generation), '[]') FROM runtime_deployment_generations g),
		'audit', (SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY a.created_at, a.id), '[]') FROM admin_audit_log a))::text`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

// generationsAudit lists the audited deployment actions in order.
func generationsAudit(t *testing.T, f fixture) []string {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), "SELECT action FROM admin_audit_log WHERE resource_type='sandbox_deployment' ORDER BY created_at, id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return actions
}

// generationsCredential returns the stored sealed credential after checking
// the deployment is at generation.
func generationsCredential(t *testing.T, f fixture, generation uint64) []byte {
	t.Helper()
	var stored int64
	var sealed []byte
	if err := f.pool.QueryRow(t.Context(), "SELECT generation,provider_credential FROM runtime_deployment").Scan(&stored, &sealed); err != nil || uint64(stored) != generation || len(sealed) == 0 {
		t.Fatal("stored credential", stored, len(sealed), err)
	}
	return sealed
}

func TestE2BChangeClassifierOmittedKeyAndExplicitSameKey(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	input := setupE2BSelection()
	id, _ := f.initialize(t, changes, input)
	configuration := input.Configuration.(*e2b.DeploymentConfiguration)
	key := configuration.APIKey
	configuration.APIKey = ""
	input.Resources = sandbox.Resources{}
	input.ExpectedGeneration = 1
	resolved, unchanged, err := changes.ClassifyChange(t.Context(), id, input)
	if err != nil || !unchanged || resolved.Configuration.(*e2b.DeploymentConfiguration).APIKey != key || resolved.Resources.CPUs == 0 {
		t.Fatal(unchanged, err)
	}
	configuration.APIKey = key
	configuration.CredentialSupplied = true
	if _, unchanged, err = changes.ClassifyChange(t.Context(), id, input); err != nil || unchanged {
		t.Fatal("explicit same key skipped verification", err)
	}
	invalid := input
	invalid.Runtime = &sandbox.RuntimeRelease{}
	if _, _, err := changes.ClassifyChange(t.Context(), id, invalid); err == nil {
		t.Fatal("omitted resources erased forbidden Runtime input")
	}
	input.ExpectedGeneration = 0
	_, _, err = changes.ClassifyChange(t.Context(), id, input)
	var stale *deployment.GenerationStaleError
	if !errors.As(err, &stale) {
		t.Fatal("stale did not precede no-op", err)
	}
}

func TestGenerationPinRetainsOfflineZeroResourceFallback(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	_, view := f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 4, MaxRetained: 16})
	f.connect(t, node.NodeID)
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1`, node.NodeID); err != nil {
		t.Fatal(err)
	}
	// Retain the generation and advance the deployment directly, isolating the
	// persistence invariant from the node generation protocol.
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	q := sqlc.New(tx)
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
	if err = changes.CollectGenerations(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := f.service.GenerationPage(t.Context(), -1)
	if err != nil || len(rows) != 1 {
		t.Fatal("offline pin was collected", rows, err)
	}
	nodes, err := f.service.ListNodes(t.Context())
	if err != nil || len(nodes) != 1 || nodes[0].Rollout.State != "unknown" || nodes[0].Rollout.ReadyGeneration == nil || *nodes[0].Rollout.ReadyGeneration != 1 {
		t.Fatal(nodes, err)
	}
	if _, err = f.pool.Exec(t.Context(), `UPDATE runtime_nodes SET removed_at=clock_timestamp(),ready_generation=NULL WHERE id=$1`, node.NodeID); err != nil {
		t.Fatal(err)
	}
	if err = changes.CollectGenerations(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err = f.service.GenerationPage(t.Context(), -1)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}

// Two changes submitted against the same generation: one commits, the other
// reports the generation it lost to.
func TestConcurrentUpdatesAtOneGenerationHaveOneWinner(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	input := sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")}
	id, view := f.initialize(t, changes, input)
	type outcome struct {
		view deployment.View
		cpus uint32
		err  error
	}
	outcomes := make(chan outcome, 2)
	start := make(chan struct{})
	for i := 1; i <= 2; i++ {
		change := input
		change.Resources.CPUs += uint32(i)
		change.ExpectedGeneration = view.Generation
		go func() {
			<-start
			committed, err := changes.Update(admin(t), id, change)
			outcomes <- outcome{view: committed, cpus: change.Resources.CPUs, err: err}
		}()
	}
	close(start)
	var winners []outcome
	for range 2 {
		o := <-outcomes
		var stale *deployment.GenerationStaleError
		switch {
		case o.err == nil:
			winners = append(winners, o)
		case errors.As(o.err, &stale):
			if stale.CurrentGeneration != view.Generation+1 {
				t.Fatal("stale change reported the wrong current generation", stale.CurrentGeneration)
			}
		default:
			t.Fatal(o.err)
		}
	}
	if len(winners) != 1 {
		t.Fatal("changes at one generation did not have exactly one winner", len(winners))
	}
	winner := winners[0]
	if winner.view.Generation != view.Generation+1 || winner.view.Specification == nil || winner.view.Specification.Resources.CPUs != winner.cpus {
		t.Fatal("winning change was not committed", winner.view)
	}
	current, err := f.service.View(t.Context())
	if err != nil || current.Generation != view.Generation+1 || current.Specification == nil || current.Specification.Resources.CPUs != winner.cpus {
		t.Fatal("the losing change overwrote the winner", current, err)
	}
	if audit := generationsAudit(t, f); !slices.Equal(audit, []string{"change"}) {
		t.Fatal("audit does not record exactly the winning change", audit)
	}
}

// A replaced E2B key is sealed for the new generation only; an omitted key
// keeps the current one, sealed again for the next generation.
func TestE2BCredentialReplacementSealsTheNewGeneration(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	input := setupE2BSelection()
	configured := input.Configuration.(*e2b.DeploymentConfiguration)
	id, view := f.initialize(t, changes, input)
	open := func(sealed []byte, generation uint64) (string, error) {
		secret, err := f.cipher.OpenSandboxDeployment(sealed, id, generation)
		return string(secret), err
	}
	first := generationsCredential(t, f, view.Generation)
	if key, err := open(first, view.Generation); err != nil || key != configured.APIKey {
		t.Fatal("initial credential was not sealed for its generation", err)
	}

	replacement := "replacement-private-api-key"
	replace := sandbox.Selection{DeploymentSpec: input.DeploymentSpec, Provider: "e2b", ExpectedGeneration: view.Generation,
		Configuration: &e2b.DeploymentConfiguration{APIKey: replacement, CredentialSupplied: true, Template: configured.Template}}
	replaced, err := changes.Update(admin(t), id, replace)
	if err != nil || replaced.Generation != view.Generation+1 || !replaced.CredentialConfigured {
		t.Fatal("credential replacement was not committed", replaced, err)
	}
	second := generationsCredential(t, f, replaced.Generation)
	if bytes.Equal(second, first) {
		t.Fatal("replacement kept the previous ciphertext")
	}
	if key, err := open(second, replaced.Generation); err != nil || key != replacement {
		t.Fatal("replacement was not sealed for the new generation", err)
	}
	if _, err := open(second, view.Generation); err == nil {
		t.Fatal("replacement opened for the previous generation")
	}
	setup, err := f.service.Setup(t.Context())
	if err != nil || setup.Generation != replaced.Generation || setup.Configuration.(*e2b.DeploymentConfiguration).APIKey != replacement {
		t.Fatal("setup does not use the replacement", err)
	}

	omitted := sandbox.Selection{DeploymentSpec: input.DeploymentSpec, Provider: "e2b", ExpectedGeneration: replaced.Generation,
		Configuration: &e2b.DeploymentConfiguration{Template: "next:" + uuid.NewString()}}
	resolved, unchanged, err := changes.ClassifyChange(t.Context(), id, omitted)
	if err != nil || unchanged || resolved.ReplacesCredential() || resolved.Configuration.(*e2b.DeploymentConfiguration).APIKey != replacement {
		t.Fatal("omitted key did not resolve to the current key", unchanged, err)
	}
	changed, err := changes.Update(admin(t), id, resolved)
	if err != nil || changed.Generation != replaced.Generation+1 {
		t.Fatal(changed, err)
	}
	if key, err := open(generationsCredential(t, f, changed.Generation), changed.Generation); err != nil || key != replacement {
		t.Fatal("omitted key did not keep the current key", err)
	}
	setup, err = f.service.Setup(t.Context())
	if err != nil || setup.Configuration.(*e2b.DeploymentConfiguration).APIKey != replacement || setup.Configuration.(*e2b.DeploymentConfiguration).Template != omitted.Configuration.(*e2b.DeploymentConfiguration).Template {
		t.Fatal("setup lost the kept key or the new template", err)
	}
	if audit := generationsAudit(t, f); !slices.Equal(audit, []string{"replace_credential", "change"}) {
		t.Fatal("audit does not distinguish the replacement", audit)
	}
}

// A rejected change leaves the deployment, its retained generations and the
// audit as they were, including writes made before the rejection.
func TestRejectedChangeLeavesDeploymentUnchanged(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	input := sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")}
	id, view := f.initialize(t, changes, input)
	// The ready node pins the current generation, so a committed change also
	// retains it.
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	f.connect(t, node.NodeID)
	before := generationsState(t, f)
	change := input
	change.Resources.CPUs++
	change.ExpectedGeneration = view.Generation
	// The audit write comes last and fails without administrator provenance.
	if _, err := changes.Update(t.Context(), id, change); !errors.Is(err, adminaudit.ErrInvalidSource) {
		t.Fatal("change without administrator provenance", err)
	}
	if after := generationsState(t, f); after != before {
		t.Fatal("failed audit write left the change behind", after)
	}
	other := sandbox.Selection{Provider: "microsandbox", DeploymentSpec: testSpecification("microsandbox"), ExpectedGeneration: view.Generation}
	var reset *deployment.ResetRequiredError
	if _, err := changes.Update(admin(t), id, other); !errors.As(err, &reset) || reset.CurrentProvider != "docker" || reset.RequestedProvider != "microsandbox" {
		t.Fatal("backend change without a reset", err)
	}
	if after := generationsState(t, f); after != before {
		t.Fatal("backend change without a reset changed the deployment", after)
	}
	committed, err := changes.Update(admin(t), id, change)
	if err != nil || committed.Generation != view.Generation+1 {
		t.Fatal(committed, err)
	}
	retained, err := f.service.GenerationPage(t.Context(), -1)
	if err != nil || len(retained) != 1 || retained[0].Generation != view.Generation {
		t.Fatal("committed change did not retain the pinned generation", retained, err)
	}
	if audit := generationsAudit(t, f); !slices.Equal(audit, []string{"change"}) {
		t.Fatal("committed change was not audited", audit)
	}
}
