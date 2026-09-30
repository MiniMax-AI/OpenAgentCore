package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

// SandboxCredentialAllocationPage reads owned receipts without granting execution authority.
func (s *Store) SandboxCredentialAllocationPage(ctx context.Context, after string) ([]RuntimeAllocation, error) {
	id := pgtype.UUID{Valid: true}
	if after != "" {
		var err error
		id, err = parseID(after)
		if err != nil {
			return nil, err
		}
	}
	rows, err := s.queries.ListRuntimeAllocations(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]RuntimeAllocation, 0, len(rows))
	for _, r := range rows {
		out = append(out, runtimeAllocationFromRow(r.RuntimeAllocation, r.SessionID, r.TenantID, r.DeletedAt, r.Expired))
	}
	return out, nil
}
