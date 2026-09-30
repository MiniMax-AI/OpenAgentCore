package postgresreader

import (
	"context"
	"errors"
	"math"
	"regexp"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/google/uuid"
)

var providerPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

func (r *Reader) Export(ctx context.Context, record runtimeobs.ExportRecord) error {
	if record.CollectionSource != runtimeobs.CollectionSourcePeriodic {
		return nil
	}
	if err := validateRecord(record); err != nil {
		return err
	}
	if record.ResolvedAt.Before(r.now().Add(-retention)) {
		return nil
	}
	// Counters without a provider incarnation cannot safely participate in rates.
	if record.Sample != nil && record.Sample.StartedAt == nil {
		value := *record.Sample
		value.CPUUsageSecondsTotal = nil
		value.CPUCapacityCores = nil
		value.CPUUtilizationRatio = nil
		value.MemoryUsageBytes = nil
		value.MemoryLimitBytes = nil
		record.Sample = &value
	}
	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()
	if err := r.store.InsertRuntimeHistorySample(ctx, record); err != nil {
		return errors.New("persist Runtime history")
	}
	return nil
}

func validateRecord(record runtimeobs.ExportRecord) error {
	invalid := errors.New("invalid Runtime history sample")
	for _, value := range []string{record.TenantID, record.SessionID, record.EnvironmentID} {
		if !validID(value) {
			return invalid
		}
	}
	if record.AllocationID != "" && !validID(record.AllocationID) {
		return invalid
	}
	if record.Mode != runtimeobs.ModeManaged || record.CollectionSource != runtimeobs.CollectionSourcePeriodic || !validTime(record.ResolvedAt) || record.ProviderType != "" && !providerPattern.MatchString(record.ProviderType) {
		return invalid
	}
	if record.Status != runtimeobs.StatusObserved && record.Status != runtimeobs.StatusUnavailable && record.Status != runtimeobs.StatusUnsupported {
		return invalid
	}
	if (record.Status == runtimeobs.StatusObserved) != (record.Sample != nil) {
		return invalid
	}
	if value := record.Sample; value != nil {
		if !validTime(value.ObservedAt) || value.ObservedAt.After(record.ResolvedAt) {
			return invalid
		}
		if value.StartedAt != nil && (!validTime(*value.StartedAt) || value.StartedAt.After(value.ObservedAt) || record.AllocationID == "" || record.ProviderType == "") {
			return invalid
		}
		if value.CPUUsageSecondsTotal != nil && (!validFloat(*value.CPUUsageSecondsTotal) || *value.CPUUsageSecondsTotal < 0) {
			return invalid
		}
		if value.CPUCapacityCores != nil && (!validFloat(*value.CPUCapacityCores) || *value.CPUCapacityCores <= 0) {
			return invalid
		}
		if value.CPUUtilizationRatio != nil && (!validFloat(*value.CPUUtilizationRatio) || *value.CPUUtilizationRatio < 0) {
			return invalid
		}
		if value.MemoryUsageBytes != nil && *value.MemoryUsageBytes > maxSafeInteger {
			return invalid
		}
		if value.MemoryLimitBytes != nil && (*value.MemoryLimitBytes == 0 || *value.MemoryLimitBytes > maxSafeInteger) {
			return invalid
		}
	}
	if value := record.TokenUsage; value != nil && (value.InputTokens > maxSafeInteger || value.OutputTokens > maxSafeInteger) {
		return invalid
	}
	return nil
}

const maxSafeInteger = uint64(1<<53 - 1)

func validID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil && parsed.String() == value
}
func validTime(value time.Time) bool {
	return !value.IsZero() && value.UnixNano() >= 0 && value.Equal(time.Unix(0, value.UnixNano()))
}
func validFloat(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
