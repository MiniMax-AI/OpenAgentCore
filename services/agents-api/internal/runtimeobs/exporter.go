package runtimeobs

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	defaultExportQueueCapacity = 256
	defaultExportTimeout       = 2 * time.Second
)

// ExportRecord is the provider-neutral, sanitized history handoff produced
// after Runtime identity and a current observation result have been validated.
// It intentionally excludes provider receipts, native identifiers, and raw
// provider errors.
type ExportRecord struct {
	TenantID      string
	SessionID     string
	EnvironmentID string
	AllocationID  string

	Mode             Mode
	ProviderType     string
	Status           Status
	Reason           string
	CollectionSource CollectionSource

	ResolvedAt     time.Time
	SourceDuration time.Duration
	Sample         *Sample
	TokenUsage     *TokenUsage
}

// Exporter persists or forwards sanitized Runtime observation records. Core
// invokes exporters only through a bounded asynchronous dispatcher, so an
// exporter outage cannot fail or block the current-observation request path.
type Exporter interface {
	Export(context.Context, ExportRecord) error
}

type ExportOptions struct {
	QueueCapacity int
	Timeout       time.Duration
}

type ServiceOption func(*serviceOptions) error

type exporterConfig struct {
	exporter      Exporter
	exportOptions ExportOptions
}

type serviceOptions struct {
	exporters []exporterConfig
}

// WithExporter enables best-effort history export. Queue saturation drops the
// newest handoff; it never changes the Runtime observation result.
func WithExporter(exporter Exporter, options ExportOptions) ServiceOption {
	return func(config *serviceOptions) error {
		if exporter == nil {
			return errors.New("Runtime observation exporter is required")
		}
		if options.QueueCapacity < 0 {
			return errors.New("Runtime observation export queue capacity cannot be negative")
		}
		if options.Timeout < 0 {
			return errors.New("Runtime observation export timeout cannot be negative")
		}
		config.exporters = append(config.exporters, exporterConfig{exporter: exporter, exportOptions: options})
		return nil
	}
}

type exportDispatcher struct {
	exporter Exporter
	timeout  time.Duration
	queue    chan ExportRecord
	done     chan struct{}

	mu     sync.Mutex
	closed bool
	cancel context.CancelFunc
}

func newExportDispatcher(exporter Exporter, options ExportOptions) *exportDispatcher {
	capacity := options.QueueCapacity
	if capacity == 0 {
		capacity = defaultExportQueueCapacity
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = defaultExportTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	dispatcher := &exportDispatcher{
		exporter: exporter,
		timeout:  timeout,
		queue:    make(chan ExportRecord, capacity),
		done:     make(chan struct{}),
		cancel:   cancel,
	}
	go dispatcher.run(ctx)
	return dispatcher
}

func (d *exportDispatcher) enqueue(record ExportRecord) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	select {
	case d.queue <- record:
	default:
	}
}

func (d *exportDispatcher) run(ctx context.Context) {
	defer close(d.done)
	for record := range d.queue {
		exportCtx, cancel := context.WithTimeout(ctx, d.timeout)
		exportSafely(exportCtx, d.exporter, record)
		cancel()
		if ctx.Err() != nil {
			return
		}
	}
}

func exportSafely(ctx context.Context, exporter Exporter, record ExportRecord) {
	defer func() {
		_ = recover()
	}()
	_ = exporter.Export(ctx, record)
}

func (d *exportDispatcher) close(ctx context.Context) error {
	d.mu.Lock()
	if !d.closed {
		d.closed = true
		close(d.queue)
	}
	d.mu.Unlock()

	select {
	case <-d.done:
		d.cancel()
		return nil
	case <-ctx.Done():
		d.cancel()
		return ctx.Err()
	}
}

func exportRecord(observation Observation, collectionSource CollectionSource) ExportRecord {
	return ExportRecord{
		TenantID:         observation.Target.TenantID,
		SessionID:        observation.Target.SessionID,
		EnvironmentID:    observation.Target.EnvironmentID,
		AllocationID:     observation.Target.Instance.AllocationID,
		Mode:             observation.Target.Mode,
		ProviderType:     observation.ProviderType,
		Status:           observation.Status,
		Reason:           observation.Reason,
		CollectionSource: collectionSource,
		ResolvedAt:       observation.ResolvedAt,
		SourceDuration:   observation.SourceDuration,
		Sample:           cloneSample(observation.Sample),
		TokenUsage:       cloneTokenUsage(observation.Target.TokenUsage),
	}
}

func cloneTokenUsage(usage *TokenUsage) *TokenUsage {
	if usage == nil {
		return nil
	}
	cloned := *usage
	return &cloned
}

func cloneSample(sample *Sample) *Sample {
	if sample == nil {
		return nil
	}
	cloned := *sample
	if sample.StartedAt != nil {
		value := *sample.StartedAt
		cloned.StartedAt = &value
	}
	if sample.CPUUsageSecondsTotal != nil {
		value := *sample.CPUUsageSecondsTotal
		cloned.CPUUsageSecondsTotal = &value
	}
	if sample.CPUCapacityCores != nil {
		value := *sample.CPUCapacityCores
		cloned.CPUCapacityCores = &value
	}
	if sample.MemoryUsageBytes != nil {
		value := *sample.MemoryUsageBytes
		cloned.MemoryUsageBytes = &value
	}
	if sample.MemoryLimitBytes != nil {
		value := *sample.MemoryLimitBytes
		cloned.MemoryLimitBytes = &value
	}
	if sample.CPUUtilizationRatio != nil {
		value := *sample.CPUUtilizationRatio
		cloned.CPUUtilizationRatio = &value
	}
	if sample.DiskUsageBytes != nil {
		value := *sample.DiskUsageBytes
		cloned.DiskUsageBytes = &value
	}
	if sample.DiskLimitBytes != nil {
		value := *sample.DiskLimitBytes
		cloned.DiskLimitBytes = &value
	}
	return &cloned
}
