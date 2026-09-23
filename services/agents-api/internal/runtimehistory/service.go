package runtimehistory

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidRange  = errors.New("invalid Runtime history range")
	ErrInvalidResult = errors.New("invalid Runtime history result")
	ErrUnavailable   = errors.New("Runtime history is unavailable")
	ErrUnsupported   = errors.New("Runtime history is unsupported for this Session")
)

type ScopeResolver interface {
	ResolveRuntimeHistoryScope(context.Context, string, string) (Scope, error)
}

type Reader interface {
	Capabilities() Capabilities
	Query(context.Context, Query) (Result, error)
}

type Service struct {
	resolver     ScopeResolver
	reader       Reader
	capabilities Capabilities
	now          func() time.Time
}

func NewService(resolver ScopeResolver, reader Reader) (*Service, error) {
	if resolver == nil || reader == nil {
		return nil, errors.New("Runtime history resolver and reader are required")
	}
	capabilities := reader.Capabilities()
	if err := capabilities.Validate(); err != nil {
		return nil, err
	}
	return &Service{resolver: resolver, reader: reader, capabilities: cloneCapabilities(capabilities), now: time.Now}, nil
}

func (s *Service) Capabilities() Capabilities {
	return cloneCapabilities(s.capabilities)
}

// QuerySession resolves authenticated Core ownership before issuing any backend
// request. The history Reader receives only Core identity and bounded range data;
// provider-native identity is never an authority-bearing input.
func (s *Service) QuerySession(ctx context.Context, tenantID, sessionID string, requested Range) (Response, error) {
	requestNow := s.now().UTC()
	if !validPublicBoundary(requested.Start) || !validPublicBoundary(requested.End) || !requested.End.After(requested.Start) || requested.End.After(requestNow.Add(time.Second)) || requested.End.Sub(requested.Start) > s.capabilities.MaximumRange || requested.MaxPoints < 2 || requested.MaxPoints > s.capabilities.MaximumPoints {
		return Response{}, ErrInvalidRange
	}
	scope, err := s.resolver.ResolveRuntimeHistoryScope(ctx, tenantID, sessionID)
	if err != nil {
		return Response{}, err
	}
	if scope.TenantID != tenantID || scope.SessionID != sessionID {
		return Response{}, ErrInvalidResult
	}
	if err := scope.validate(); err != nil {
		return Response{}, ErrInvalidResult
	}
	step := resolution(requested.End.Sub(requested.Start), requested.MaxPoints, s.capabilities.MinimumStep)
	query := Query{
		Scope: scope, Start: requested.Start.UTC(), End: requested.End.UTC(), Step: step, Retention: s.capabilities.Retention, MaxPoints: requested.MaxPoints,
		MaximumSeries: s.capabilities.MaximumSeries, MaximumTotalPoints: s.capabilities.MaximumTotalPoints,
	}
	result, err := s.reader.Query(ctx, query)
	if err != nil {
		return Response{}, ErrUnavailable
	}
	if err := validateResult(query, result, s.now().UTC()); err != nil {
		return Response{}, ErrInvalidResult
	}
	return Response{
		Capabilities: cloneCapabilities(s.capabilities),
		Scope:        scope,
		Requested:    Range{Start: query.Start, End: query.End, MaxPoints: query.MaxPoints},
		Resolution:   query.Step,
		GeneratedAt:  result.GeneratedAt.UTC(),
		RetainedFrom: cloneTime(result.RetainedFrom),
		Coverage:     append([]CoveragePoint(nil), result.Coverage...),
		Series:       cloneSeries(result.Series),
		TokenUsage:   append([]TokenUsagePoint(nil), result.TokenUsage...),
	}, nil
}

func resolution(duration time.Duration, maxPoints int, minimum time.Duration) time.Duration {
	divisor := time.Duration(maxPoints)
	step := duration / divisor
	if duration%divisor != 0 {
		step++
	}
	if step < minimum {
		return minimum
	}
	seconds := step / time.Second
	if step%time.Second != 0 {
		seconds++
	}
	return seconds * time.Second
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneSeries(values []Series) []Series {
	result := make([]Series, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Points = append([]Point(nil), value.Points...)
		for pointIndex := range result[index].Points {
			point := &result[index].Points[pointIndex]
			point.CPUUtilizationRatio = cloneFloat(point.CPUUtilizationRatio)
			point.CPUCapacityCores = cloneFloat(point.CPUCapacityCores)
			point.MemoryUsageBytes = cloneUint64(point.MemoryUsageBytes)
			point.MemoryLimitBytes = cloneUint64(point.MemoryLimitBytes)
		}
	}
	return result
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
