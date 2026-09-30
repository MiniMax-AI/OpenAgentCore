package store_test

import (
	"context"
	"crypto/sha256"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/projectpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
)

// fixtureProjects builds the Project adapter and service on db.
func fixtureProjects(t testing.TB, db fixtureDB) (*projectpg.Store, *projects.Service) {
	t.Helper()
	projectStore := projectpg.New(pgunit.NewPool(db.pool))
	projectService, err := projects.NewService(projectStore)
	if err != nil {
		t.Fatal(err)
	}
	return projectStore, projectService
}

// fixtureProjectsReader reads Projects from the database and resolves Project
// keys from the test's fixture keys.
type fixtureProjectsReader struct {
	projects.Reader
	keys fixtureKeyResolver
}

func (p fixtureProjectsReader) ResolveAPIKey(ctx context.Context, digest [sha256.Size]byte) (projects.KeyBinding, error) {
	return p.keys.ResolveAPIKey(ctx, digest)
}
