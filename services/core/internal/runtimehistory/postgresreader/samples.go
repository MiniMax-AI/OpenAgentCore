package postgresreader

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// samples stores Runtime history samples in Core's database.
type samples struct {
	units *pgunit.Pool
}

// InsertRuntimeHistorySample copies a sanitized periodic observation. Public
// Session/Turn Usage remains the accounting authority; these measured counters
// are only sampled chart values. Deleted or mismatched owners cannot create orphan rows.
func (s samples) InsertRuntimeHistorySample(ctx context.Context, record runtimeobs.ExportRecord) error {
	tenant, err := pgunit.ParseID(record.TenantID)
	if err != nil {
		return err
	}
	session, err := pgunit.ParseID(record.SessionID)
	if err != nil {
		return err
	}
	environment, err := pgunit.ParseID(record.EnvironmentID)
	if err != nil {
		return err
	}
	params := sqlc.InsertRuntimeHistorySampleParams{
		TenantID: tenant, SessionID: session, EnvironmentID: environment,
		ResolvedAtNs: record.ResolvedAt.UnixNano(), ProviderType: record.ProviderType, Status: string(record.Status),
	}
	if record.AllocationID != "" {
		params.AllocationID, err = pgunit.ParseID(record.AllocationID)
		if err != nil {
			return err
		}
	}
	if sample := record.Sample; sample != nil {
		params.ObservedAtNs = pgtype.Int8{Int64: sample.ObservedAt.UnixNano(), Valid: true}
		if sample.StartedAt != nil {
			params.StartedAtNs = pgtype.Int8{Int64: sample.StartedAt.UnixNano(), Valid: true}
		}
		params.CpuUsageSeconds = historyFloat(sample.CPUUsageSecondsTotal)
		params.CpuCapacityCores = historyFloat(sample.CPUCapacityCores)
		params.CpuUtilizationRatio = historyFloat(sample.CPUUtilizationRatio)
		params.MemoryUsageBytes = historyInteger(sample.MemoryUsageBytes)
		params.MemoryLimitBytes = historyInteger(sample.MemoryLimitBytes)
	}
	if usage := record.TokenUsage; usage != nil {
		params.InputTokens = historyInteger(&usage.InputTokens)
		params.OutputTokens = historyInteger(&usage.OutputTokens)
	}
	return s.units.Queries().InsertRuntimeHistorySample(ctx, params)
}

func (s samples) ListRuntimeHistorySamples(ctx context.Context, tenantID, sessionID, environmentID string, startNS, endNS int64, limit int32) ([]runtimeobs.ExportRecord, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return nil, err
	}
	session, err := pgunit.ParseID(sessionID)
	if err != nil {
		return nil, err
	}
	environment, err := pgunit.ParseID(environmentID)
	if err != nil {
		return nil, err
	}
	rows, err := s.units.Queries().ListRuntimeHistorySamples(ctx, sqlc.ListRuntimeHistorySamplesParams{
		TenantID: tenant, SessionID: session, EnvironmentID: environment, StartNs: startNS, EndNs: endNS, RowLimit: limit,
	})
	if err != nil {
		return nil, err
	}
	records := make([]runtimeobs.ExportRecord, 0, len(rows))
	for _, row := range rows {
		record := runtimeobs.ExportRecord{
			TenantID: tenantID, SessionID: sessionID, EnvironmentID: environmentID,
			Mode: runtimeobs.ModeManaged, CollectionSource: runtimeobs.CollectionSourcePeriodic,
			ResolvedAt: time.Unix(0, row.ResolvedAtNs).UTC(), ProviderType: row.ProviderType, Status: runtimeobs.Status(row.Status),
		}
		if row.AllocationID.Valid {
			record.AllocationID = uuid.UUID(row.AllocationID.Bytes).String()
		}
		if row.ObservedAtNs.Valid {
			record.Sample = &runtimeobs.Sample{
				ObservedAt:           time.Unix(0, row.ObservedAtNs.Int64).UTC(),
				CPUUsageSecondsTotal: historyFloatPointer(row.CpuUsageSeconds), CPUCapacityCores: historyFloatPointer(row.CpuCapacityCores),
				CPUUtilizationRatio: historyFloatPointer(row.CpuUtilizationRatio),
				MemoryUsageBytes:    historyIntegerPointer(row.MemoryUsageBytes), MemoryLimitBytes: historyIntegerPointer(row.MemoryLimitBytes),
			}
			if row.StartedAtNs.Valid {
				value := time.Unix(0, row.StartedAtNs.Int64).UTC()
				record.Sample.StartedAt = &value
			}
		}
		if row.InputTokens.Valid && row.OutputTokens.Valid {
			record.TokenUsage = &runtimeobs.TokenUsage{InputTokens: uint64(row.InputTokens.Int64), OutputTokens: uint64(row.OutputTokens.Int64)}
		}
		records = append(records, record)
	}
	return records, nil
}

// PruneRuntimeHistorySamples deletes one batch of expired Runtime samples and
// one batch of expired node-host samples, which share the retention.
func (s samples) PruneRuntimeHistorySamples(ctx context.Context, beforeNS int64) (int64, error) {
	queries := s.units.Queries()
	count, err := queries.PruneRuntimeHistorySamples(ctx, beforeNS)
	if err != nil {
		return count, err
	}
	nodes, err := queries.PruneNodeHostHistory(ctx, pgtype.Timestamptz{Time: time.Unix(0, beforeNS), Valid: true})
	return count + nodes, err
}

func historyFloat(value *float64) pgtype.Float8 {
	if value == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *value, Valid: true}
}
func historyInteger(value *uint64) pgtype.Int8 {
	if value == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: int64(*value), Valid: true}
}
func historyFloatPointer(value pgtype.Float8) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}
func historyIntegerPointer(value pgtype.Int8) *uint64 {
	if !value.Valid {
		return nil
	}
	result := uint64(value.Int64)
	return &result
}
