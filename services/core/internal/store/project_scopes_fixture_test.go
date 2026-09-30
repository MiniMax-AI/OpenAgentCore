package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrProjectScopeConflict = errors.New("fixture project conflicts with a persisted execution scope")

// EnsureProjectScopes establishes execution scopes for legacy resource fixtures.
func (s *Store) EnsureProjectScopes(ctx context.Context, scopes []identity.ProjectScope) error {
	validated, err := identity.ProjectScopes(scopes)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		for _, scope := range validated {
			tenant, err := parseID(scope.TenantID)
			if err != nil {
				return err
			}
			_, err = q.EnsureProjectScope(ctx, sqlc.EnsureProjectScopeParams{TenantID: tenant, OrganizationID: scope.OrganizationID, ProjectID: scope.ProjectID})
			var databaseError *pgconn.PgError
			if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &databaseError) && databaseError.Code == "23505") {
				return ErrProjectScopeConflict
			}
			if err != nil {
				return fmt.Errorf("verify execution project scope: %w", err)
			}
		}
		return nil
	})
}
