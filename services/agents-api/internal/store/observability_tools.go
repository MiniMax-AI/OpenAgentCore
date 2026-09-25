package store

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/observability"
)

const insertToolAttempt = `INSERT INTO observability_tool_attempts
    (id, tenant_id, session_id, turn_id, started_at, finished_at, tool_category, outcome, duration_ms)
SELECT $1::uuid, s.tenant_id, s.id, t.id, $5::timestamptz, $6, $7, $8, $9::bigint
FROM sessions s JOIN turns t ON t.session_id = s.id
WHERE s.tenant_id = $2::uuid AND s.id = $3::uuid AND t.id = $4::uuid
ON CONFLICT (id) DO NOTHING`

// WriteToolAttempts stores only sanitized terminal tool evidence. Duplicate
// native completion events have one deterministic attempt ID.
func (s *Store) WriteToolAttempts(ctx context.Context, attempts []observability.ToolAttempt, dropped, failed int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var inserted int64
	for _, attempt := range attempts {
		result, err := tx.Exec(ctx, insertToolAttempt, attempt.ID, attempt.TenantID, attempt.SessionID, attempt.TurnID,
			attempt.StartedAt, attempt.FinishedAt, attempt.Category, attempt.Outcome, attempt.DurationMS)
		if err != nil {
			return err
		}
		inserted += result.RowsAffected()
	}
	if len(attempts) > 0 || dropped > 0 || failed > 0 {
		_, err = tx.Exec(ctx, `INSERT INTO observability_collector_minute_buckets AS current
            (bucket_start, source, attempted_count, observed_count, dropped_count, export_failed_count)
            VALUES ($1, 'tool', $2, $3, $4, $5)
            ON CONFLICT (bucket_start, source) DO UPDATE SET
              attempted_count = current.attempted_count + EXCLUDED.attempted_count,
              observed_count = current.observed_count + EXCLUDED.observed_count,
              dropped_count = current.dropped_count + EXCLUDED.dropped_count,
              export_failed_count = current.export_failed_count + EXCLUDED.export_failed_count`,
			time.Now().UTC().Truncate(time.Minute), int64(len(attempts))+dropped, inserted, dropped, failed)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
