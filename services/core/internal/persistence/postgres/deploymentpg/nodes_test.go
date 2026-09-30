package deploymentpg_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// nodesExec changes rows the node operations read, standing in for state they
// cannot reach through their own API.
func nodesExec(t *testing.T, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

// nodesTokenDigest is the stored form of an enrollment token.
func nodesTokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

// nodesEnrollment is a valid enrollment of a new node on view.
func nodesEnrollment(view deployment.View, name, credential string) deployment.Enrollment {
	return deployment.Enrollment{NodeID: uuid.NewString(), Name: name, Credential: strings.Repeat(credential, 64), Provider: view.Provider, BackendFingerprint: strings.Repeat("b", 64),
		SpecificationDigest: view.SpecificationDigest, DeploymentGeneration: view.Generation, CoreURL: fixturePublicURL}
}

func TestEnrollmentApprovedCapacity(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	_, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: testSpecification("microsandbox")})
	for _, capacity := range []deployment.Capacity{{MaxActive: 0, MaxRetained: 8}, {MaxActive: 3, MaxRetained: 2}, {MaxActive: 1, MaxRetained: 1000001}} {
		if _, err := f.service.CreateEnrollment(t.Context(), capacity); !errors.Is(err, deployment.ErrInvalidInput) {
			t.Fatal("invalid capacity accepted", err)
		}
	}
	token, err := f.service.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 1, MaxRetained: 3})
	if err != nil {
		t.Fatal(err)
	}
	config, err := f.service.NodeConfiguration(t.Context(), "", token.Token, 0)
	if err != nil || config.MaxActive != 1 || config.MaxRetained != 3 {
		t.Fatal("bootstrap lost approved capacity", config, err)
	}
	input := nodesEnrollment(view, "approved", "a")
	identity, err := f.service.Enroll(t.Context(), token.Token, input)
	if err != nil || identity.MaxActive != 1 || identity.MaxRetained != 3 {
		t.Fatal("enrollment did not apply token capacity", identity, err)
	}
	if _, err := f.service.Enroll(t.Context(), token.Token, input); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("consumed token reused", err)
	}
	if err := f.service.UpdateNode(t.Context(), input.NodeID, deployment.NodeUpdate{Name: "approved", MaxActive: 2, MaxRetained: 5}); err != nil {
		t.Fatal(err)
	}
	// Microsandbox uses both limits, so a retained limit below the active one is rejected and nothing is written.
	if err := f.service.UpdateNode(t.Context(), input.NodeID, deployment.NodeUpdate{Name: "rejected", MaxActive: 4, MaxRetained: 3}); !errors.Is(err, deployment.ErrInvalidInput) {
		t.Fatal("retained limit below active accepted", err)
	}
	var name string
	if err := f.pool.QueryRow(t.Context(), "SELECT name FROM runtime_nodes WHERE id=$1", input.NodeID).Scan(&name); err != nil || name != "approved" {
		t.Fatal("rejected update was written", name, err)
	}
	identity, err = f.service.AuthenticateNode(t.Context(), input.NodeID, input.Credential)
	if err != nil || identity.MaxActive != 2 || identity.MaxRetained != 5 {
		t.Fatal("credential read ignored admin update", identity, err)
	}
	config, err = f.service.NodeConfiguration(t.Context(), input.NodeID, input.Credential, 0)
	if err != nil || config.MaxActive != 2 || config.MaxRetained != 5 {
		t.Fatal("configuration read ignored admin update", config, err)
	}
}

// Docker never suspends a sandbox, so every read reports its retained limit as its
// active limit, including for a token or node stored before Core applied that rule.
func TestDockerRetainedLimitFollowsActive(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	_, view := f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	token, err := f.service.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 3, MaxRetained: 1})
	if err != nil {
		t.Fatal(err)
	}
	current := nodesEnrollment(view, "Docker", "d")
	if identity, err := f.service.Enroll(t.Context(), token.Token, current); err != nil || identity.MaxRetained != 3 {
		t.Fatal("enrollment kept a separate Docker retained limit", identity, err)
	}
	legacyToken, err := f.service.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 2, MaxRetained: 8})
	if err != nil {
		t.Fatal(err)
	}
	nodesExec(t, f.pool, "UPDATE runtime_node_enrollments SET max_retained=8 WHERE token_sha256=$1", nodesTokenDigest(legacyToken.Token))
	if config, err := f.service.NodeConfiguration(t.Context(), "", legacyToken.Token, 0); err != nil || config.MaxRetained != 2 {
		t.Fatal("bootstrap reported a legacy Docker retained limit", config, err)
	}
	legacy := nodesEnrollment(view, "Legacy", "l")
	if _, err := f.service.Enroll(t.Context(), legacyToken.Token, legacy); err != nil {
		t.Fatal(err)
	}
	var stored int32
	if err := f.pool.QueryRow(t.Context(), "SELECT max_retained FROM runtime_nodes WHERE id=$1", legacy.NodeID).Scan(&stored); err != nil || stored != 2 {
		t.Fatal("enrollment stored a legacy Docker retained limit", stored, err)
	}
	nodesExec(t, f.pool, "UPDATE runtime_nodes SET max_retained=8 WHERE id=$1", legacy.NodeID)
	if identity, err := f.service.AuthenticateNode(t.Context(), legacy.NodeID, legacy.Credential); err != nil || identity.MaxRetained != 2 {
		t.Fatal("node identity reported a legacy Docker retained limit", identity, err)
	}
	if config, err := f.service.NodeConfiguration(t.Context(), legacy.NodeID, legacy.Credential, 0); err != nil || config.MaxRetained != 2 {
		t.Fatal("node configuration reported a legacy Docker retained limit", config, err)
	}
	if err := f.service.UpdateNode(t.Context(), current.NodeID, deployment.NodeUpdate{Name: "Docker", MaxActive: 5, MaxRetained: 12}); err != nil {
		t.Fatal(err)
	}
	nodes, err := f.service.ListNodes(t.Context())
	if err != nil || len(nodes) != 2 {
		t.Fatal(nodes, err)
	}
	for _, n := range nodes {
		if n.MaxRetained != n.MaxActive || (n.ID == current.NodeID && n.MaxActive != 5) {
			t.Fatal("node list kept a separate Docker retained limit", n)
		}
	}
}

// An unknown or consumed token and a wrong node credential are rejected before
// any deployment state or enrollment input is judged, so they reveal nothing.
func TestNodeCredentialPrecedesDeploymentState(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	unknown := strings.Repeat("u", 64)
	// Before initialization the deployment is unavailable.
	early := deployment.Enrollment{NodeID: uuid.NewString(), Name: "early", Credential: strings.Repeat("e", 64), Provider: "docker", BackendFingerprint: strings.Repeat("b", 64),
		SpecificationDigest: strings.Repeat("d", 64), DeploymentGeneration: 1, CoreURL: fixturePublicURL}
	if _, err := f.service.Enroll(t.Context(), unknown, early); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("uninitialized deployment judged before the token", err)
	}
	if _, err := f.service.NodeConfiguration(t.Context(), "", unknown, 0); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("uninitialized deployment judged before the token", err)
	}
	if _, err := f.service.AuthenticateNode(t.Context(), early.NodeID, early.Credential); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("unknown node authenticated", err)
	}
	_, view := f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	consumed, err := f.service.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	if err != nil {
		t.Fatal(err)
	}
	node := nodesEnrollment(view, "ordered", "c")
	if _, err := f.service.Enroll(t.Context(), consumed.Token, node); err != nil {
		t.Fatal(err)
	}
	fresh, err := f.service.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*deployment.Enrollment)
		want   error
	}{
		{"provider", func(e *deployment.Enrollment) { e.Provider = "microsandbox" }, deployment.ErrInvalidInput},
		{"generation", func(e *deployment.Enrollment) { e.DeploymentGeneration++ }, deployment.ErrSpecificationMismatch},
		{"specification", func(e *deployment.Enrollment) { e.SpecificationDigest = strings.Repeat("0", 64) }, deployment.ErrSpecificationMismatch},
		{"address", func(e *deployment.Enrollment) { e.CoreURL = "https://other.example" }, deployment.ErrNodeAddressMismatch},
	} {
		input := nodesEnrollment(view, "refused", "r")
		tc.change(&input)
		for _, token := range []string{unknown, consumed.Token} {
			if _, err := f.service.Enroll(t.Context(), token, input); !errors.Is(err, deployment.ErrNodeCredential) {
				t.Fatal(tc.name, "judged before the token", err)
			}
		}
		if _, err := f.service.Enroll(t.Context(), fresh.Token, input); !errors.Is(err, tc.want) {
			t.Fatal(tc.name, err)
		}
	}
	if _, err := f.service.NodeConfiguration(t.Context(), "", unknown, 0); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("unknown token configured", err)
	}
	if _, err := f.service.NodeConfiguration(t.Context(), "", consumed.Token, 0); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("consumed token configured", err)
	}
	// None of the refusals consumed the valid token.
	if _, err := f.service.NodeConfiguration(t.Context(), "", fresh.Token, 0); err != nil {
		t.Fatal("refused enrollment consumed its token", err)
	}
	// A node whose enrollment identity no longer matches is refused only with
	// its own credential.
	nodesExec(t, f.pool, "UPDATE runtime_nodes SET specification_digest=$2 WHERE id=$1", node.NodeID, strings.Repeat("0", 64))
	wrong := strings.Repeat("w", 64)
	if _, err := f.service.AuthenticateNode(t.Context(), node.NodeID, wrong); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("identity judged before the credential", err)
	}
	if _, err := f.service.NodeStatus(t.Context(), node.NodeID, wrong); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("identity judged before the credential", err)
	}
	if _, err := f.service.NodeConfiguration(t.Context(), node.NodeID, wrong, 0); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("identity judged before the credential", err)
	}
	if _, err := f.service.AuthenticateNode(t.Context(), node.NodeID, node.Credential); !errors.Is(err, deployment.ErrSpecificationMismatch) {
		t.Fatal("mismatched identity authenticated", err)
	}
	if _, err := f.service.NodeConfiguration(t.Context(), node.NodeID, node.Credential, 0); !errors.Is(err, deployment.ErrSpecificationMismatch) {
		t.Fatal("mismatched identity configured", err)
	}
}

// A node must connect to the address Core advertises now. A refused enrollment
// registers nothing and leaves its token usable.
func TestEnrollmentAddressMismatchKeepsToken(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	_, view := f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	token, err := f.service.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	if err != nil {
		t.Fatal(err)
	}
	config, err := f.service.NodeConfiguration(t.Context(), "", token.Token, 0)
	if err != nil || config.CoreURL != fixturePublicURL {
		t.Fatal("bootstrap did not advertise the public URL", config, err)
	}
	input := nodesEnrollment(view, "addressed", "a")
	input.CoreURL = "https://other.example"
	if _, err := f.service.Enroll(t.Context(), token.Token, input); !errors.Is(err, deployment.ErrNodeAddressMismatch) {
		t.Fatal("another Core address enrolled", err)
	}
	// The public URL changed after the node read its configuration.
	_, moved := f.withPublicURL(t, "https://moved.example")
	input.CoreURL = fixturePublicURL
	if _, err := moved.Enroll(t.Context(), token.Token, input); !errors.Is(err, deployment.ErrNodeAddressMismatch) {
		t.Fatal("a previous public URL enrolled", err)
	}
	var consumed bool
	if err := f.pool.QueryRow(t.Context(), "SELECT consumed_at IS NOT NULL FROM runtime_node_enrollments WHERE token_sha256=$1", nodesTokenDigest(token.Token)).Scan(&consumed); err != nil || consumed {
		t.Fatal("address mismatch consumed the token", consumed, err)
	}
	if nodes, err := f.service.ListNodes(t.Context()); err != nil || len(nodes) != 0 {
		t.Fatal("address mismatch registered a node", nodes, err)
	}
	if _, err := f.service.Enroll(t.Context(), token.Token, input); err != nil {
		t.Fatal("token unusable after an address mismatch", err)
	}
	nodes, err := f.service.ListNodes(t.Context())
	if err != nil || len(nodes) != 1 || nodes[0].ID != input.NodeID || nodes[0].CoreURL != fixturePublicURL {
		t.Fatal(nodes, err)
	}
}

// A replayed enrollment finds its token consumed and recovers by authenticating.
// A node ID stays reserved, even after removal, and a refused ID keeps the token.
func TestEnrollmentReplay(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	_, view := f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	token, err := f.service.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	if err != nil {
		t.Fatal(err)
	}
	input := nodesEnrollment(view, "replayed", "r")
	identity, err := f.service.Enroll(t.Context(), token.Token, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Enroll(t.Context(), token.Token, input); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("replay reused the token", err)
	}
	if recovered, err := f.service.AuthenticateNode(t.Context(), input.NodeID, input.Credential); err != nil || recovered != identity {
		t.Fatal("lost response cannot recover", recovered, err)
	}
	second, err := f.service.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	if err != nil {
		t.Fatal(err)
	}
	other := input
	other.Credential = strings.Repeat("o", 64)
	for _, candidate := range []deployment.Enrollment{input, other} {
		if _, err := f.service.Enroll(t.Context(), second.Token, candidate); !errors.Is(err, deployment.ErrNodeExists) {
			t.Fatal("enrolled node ID reused", err)
		}
	}
	if err := f.service.RemoveNode(t.Context(), input.NodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Enroll(t.Context(), second.Token, input); !errors.Is(err, deployment.ErrNodeExists) {
		t.Fatal("removed node ID reused", err)
	}
	if _, err := f.service.AuthenticateNode(t.Context(), input.NodeID, input.Credential); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("removed node credential accepted", err)
	}
	if _, err := f.service.Enroll(t.Context(), second.Token, nodesEnrollment(view, "next", "n")); err != nil {
		t.Fatal("refused node ID consumed the token", err)
	}
}

func TestNodeUpdateBounds(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	_, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: testSpecification("microsandbox")})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 2, MaxRetained: 4})
	for _, tc := range []struct {
		update      deployment.NodeUpdate
		code, param string
	}{
		{deployment.NodeUpdate{Name: "", MaxActive: 1, MaxRetained: 1}, "invalid_name", "name"},
		{deployment.NodeUpdate{Name: " ", MaxActive: 1, MaxRetained: 1}, "invalid_name", "name"},
		{deployment.NodeUpdate{Name: strings.Repeat("n", 129), MaxActive: 1, MaxRetained: 1}, "invalid_name", "name"},
		{deployment.NodeUpdate{Name: "line\nbreak", MaxActive: 1, MaxRetained: 1}, "invalid_name", "name"},
		{deployment.NodeUpdate{Name: "bounded", MaxActive: 0, MaxRetained: 1}, "invalid_node_capacity", "max_active"},
		{deployment.NodeUpdate{Name: "bounded", MaxActive: 1000001, MaxRetained: 1000001}, "invalid_node_capacity", "max_active"},
		{deployment.NodeUpdate{Name: "bounded", MaxActive: 4, MaxRetained: 3}, "invalid_node_capacity", "max_retained"},
		{deployment.NodeUpdate{Name: "bounded", MaxActive: 1, MaxRetained: 1000001}, "invalid_node_capacity", "max_retained"},
	} {
		err := f.service.UpdateNode(t.Context(), node.NodeID, tc.update)
		var validation *deployment.NodeValidationError
		if !errors.Is(err, deployment.ErrInvalidInput) || !errors.As(err, &validation) || validation.Code != tc.code || validation.Param != tc.param {
			t.Fatal("update outside the bounds", tc.update, err)
		}
	}
	var name string
	var active, retained int32
	if err := f.pool.QueryRow(t.Context(), "SELECT name,max_active,max_retained FROM runtime_nodes WHERE id=$1", node.NodeID).Scan(&name, &active, &retained); err != nil || name != node.Name || active != 2 || retained != 4 {
		t.Fatal("rejected update was written", name, active, retained, err)
	}
	for _, update := range []deployment.NodeUpdate{{Name: strings.Repeat("n", 128), MaxActive: 1000000, MaxRetained: 1000000}, {Name: "single", MaxActive: 1, MaxRetained: 1}} {
		if err := f.service.UpdateNode(t.Context(), node.NodeID, update); err != nil {
			t.Fatal("update at the bounds rejected", update, err)
		}
	}
	nodes, err := f.service.ListNodes(t.Context())
	if err != nil || len(nodes) != 1 || nodes[0].Name != "single" || nodes[0].MaxActive != 1 || nodes[0].MaxRetained != 1 {
		t.Fatal(nodes, err)
	}
	valid := deployment.NodeUpdate{Name: "missing", MaxActive: 1, MaxRetained: 1}
	if err := f.service.UpdateNode(t.Context(), uuid.NewString(), valid); !errors.Is(err, deployment.ErrNotFound) {
		t.Fatal("unknown node updated", err)
	}
	if err := f.service.UpdateNode(t.Context(), "not-a-node", valid); !errors.Is(err, deployment.ErrInvalidInput) {
		t.Fatal("malformed node ID updated", err)
	}
	if err := f.service.RemoveNode(t.Context(), uuid.NewString()); !errors.Is(err, deployment.ErrNotFound) {
		t.Fatal("unknown node removed", err)
	}
}

func TestNodeGenerationDowngradePreservesServingProtocol(t *testing.T) {
	for _, mode := range []string{"v2", "old_v1", "current_v1"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			changes, _ := f.execution(t)
			input := sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")}
			installation, first := f.initialize(t, changes, input)
			node := f.enroll(t, first, deployment.Capacity{MaxActive: 4, MaxRetained: 16})
			f.connect(t, node.NodeID)
			switch mode {
			case "v2":
				connection := f.connect(t, node.NodeID)
				if err := f.service.HeartbeatGenerations(t.Context(), node.NodeID, connection, first.OwnerEpoch, deployment.NodeHealth{}, []sandbox.GenerationStatus{{Generation: first.Generation, SpecificationDigest: first.SpecificationDigest, State: "ready"}}); err != nil {
					t.Fatal(err)
				}
			case "old_v1":
				input.Resources.CPUs++
				input.ExpectedGeneration = first.Generation
				next, err := changes.Update(admin(t), installation, input)
				if err != nil {
					t.Fatal(err)
				}
				if next.Generation != first.Generation+1 || next.OwnerEpoch != first.OwnerEpoch {
					t.Fatal("target change replaced execution ownership", next)
				}
			}
			db := sql.OpenDB(stdlib.GetConnector(*f.pool.Config().ConnConfig))
			defer db.Close()
			migration, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../../../migrations"), goose.WithTableName("agents_api_schema_version"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = migration.DownTo(t.Context(), 81)
			if mode == "current_v1" {
				if err != nil {
					t.Fatal("safe v1 downgrade refused", err)
				}
			} else {
				if err == nil {
					t.Fatal("downgrade discarded required node protocol")
				}
				// DownTo may have removed later, reversible migrations before the
				// node protocol migration refused the downgrade. Restore the current
				// schema before using this version of the adapter to verify recovery.
				if _, err = migration.Up(t.Context()); err != nil {
					t.Fatal("refused downgrade could not restore current schema", err)
				}
				if _, err = f.service.NodeConfiguration(t.Context(), node.NodeID, node.Credential, 1); err != nil {
					t.Fatal("refused downgrade damaged retained recovery", err)
				}
				if err = f.service.RemoveNode(t.Context(), node.NodeID); err != nil {
					t.Fatal(err)
				}
				if _, err = migration.DownTo(t.Context(), 81); err != nil {
					t.Fatal("removed node blocked downgrade", err)
				}
			}
			if _, err = migration.Up(t.Context()); err != nil {
				t.Fatal("node schema could not upgrade again", err)
			}
		})
	}
}
