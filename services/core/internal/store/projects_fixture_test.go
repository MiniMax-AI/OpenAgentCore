package store

import (
	"context"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/projectpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func keyAdminContext(ctx context.Context, id string) context.Context {
	return adminaudit.WithSource(ctx, adminaudit.Source{CredentialID: "12345678", ActorLabel: "test", RequestID: uuid.NewString(), TraceID: uuid.NewString(), ProjectID: id})
}

// testProjects manages Projects in the test database, as production does.
func testProjects(t testing.TB, pool *pgxpool.Pool) *projects.Service {
	t.Helper()
	service, err := projects.NewService(projectpg.New(pgunit.NewPool(pool)))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// createTestProject creates an audited Project and returns its binding.
func createTestProject(t testing.TB, pool *pgxpool.Pool) projects.Binding {
	t.Helper()
	id := uuid.NewString()
	if _, err := testProjects(t, pool).CreateProject(keyAdminContext(t.Context(), id), projects.CreateProject{ID: id, Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	binding, err := projectpg.New(pgunit.NewPool(pool)).GetProject(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}
