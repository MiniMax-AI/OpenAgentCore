package runtimeobs

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

type fixedResolver struct {
	target Target
	err    error
}

func (r fixedResolver) Resolve(_ context.Context, tenant, session string) (Target, error) {
	target := r.target
	if target.TenantID == "" {
		target.TenantID = tenant
	}
	if target.SessionID == "" {
		target.SessionID = session
	}
	return target, r.err
}

type fixedSource struct {
	sample Sample
	err    error
	calls  int
}

func (s *fixedSource) Observe(context.Context, Target) (Sample, error) {
	s.calls++
	return s.sample, s.err
}

type typedSource struct {
	*fixedSource
	providerType string
}

func (s typedSource) ObservationProviderType() string { return s.providerType }

type blockingSource struct{}

func (blockingSource) Observe(ctx context.Context, _ Target) (Sample, error) {
	<-ctx.Done()
	return Sample{}, ctx.Err()
}

func (blockingSource) ObservationProviderType() string { return "docker" }

type channelExporter struct {
	records chan ExportRecord
	err     error
}

func (e channelExporter) Export(ctx context.Context, record ExportRecord) error {
	select {
	case e.records <- record:
		return e.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type gatedExporter struct {
	started chan struct{}
	release chan struct{}

	mu    sync.Mutex
	calls int
}

type stubbornExporter struct {
	started chan struct{}
	release chan struct{}
}

type panicExporter struct {
	called chan struct{}
}

func (e panicExporter) Export(context.Context, ExportRecord) error {
	e.called <- struct{}{}
	panic("exporter panic must remain isolated")
}

func (e stubbornExporter) Export(context.Context, ExportRecord) error {
	close(e.started)
	<-e.release
	return nil
}

func (e *gatedExporter) Export(ctx context.Context, _ ExportRecord) error {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	select {
	case e.started <- struct{}{}:
	default:
	}
	select {
	case <-e.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *gatedExporter) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func TestServiceDoesNotCallSourcesForUnsupportedModes(t *testing.T) {
	for _, mode := range []Mode{ModeNone, ModeSelfHosted} {
		source := &fixedSource{}
		target := Target{Mode: mode}
		if mode == ModeSelfHosted {
			target.EnvironmentID = "environment"
		}
		service, err := NewService(fixedResolver{target: target}, map[string]Source{"provider": source})
		if err != nil {
			t.Fatal(err)
		}
		observation, err := service.ObserveSession(t.Context(), "tenant", "session")
		if err != nil || observation.Status != StatusUnsupported || observation.Reason != "runtime_mode_not_observable" || source.calls != 0 {
			t.Fatalf("unsupported mode touched a source: %+v %v calls=%d", observation, err, source.calls)
		}
	}
}

func TestServicePreservesUnavailableAndObservedZero(t *testing.T) {
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
	service, err := NewService(fixedResolver{target: target}, nil)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusUnavailable || observation.Reason != "source_not_configured" || observation.Sample != nil {
		t.Fatalf("missing source was not unavailable: %+v %v", observation, err)
	}

	zeroCPU := float64(0)
	zeroMemory := uint64(0)
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	source := &fixedSource{sample: Sample{ObservedAt: now, CPUUsageSecondsTotal: &zeroCPU, MemoryUsageBytes: &zeroMemory}}
	service, err = NewService(fixedResolver{target: target}, map[string]Source{"provider": source})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	observation, err = service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusObserved || observation.Sample == nil || observation.Sample.CPUUsageSecondsTotal == nil || observation.Sample.MemoryUsageBytes == nil {
		t.Fatalf("observed zero was lost: %+v %v", observation, err)
	}
}

func TestServiceExportsOnlySanitizedValidatedRecords(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	startedAt := now.Add(-time.Minute)
	cpuSeconds := 12.5
	cpuCapacity := 2.0
	memoryUsage := uint64(1024)
	memoryLimit := uint64(2048)
	target := Target{
		TenantID: "tenant", SessionID: "session", EnvironmentID: "environment", Mode: ModeManaged,
		Instance: Instance{
			AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running",
			ProviderState: json.RawMessage(`{"native_id":"must-not-export"}`),
		},
	}
	source := typedSource{fixedSource: &fixedSource{sample: Sample{
		ObservedAt: now, StartedAt: &startedAt,
		CPUUsageSecondsTotal: &cpuSeconds, CPUCapacityCores: &cpuCapacity,
		MemoryUsageBytes: &memoryUsage, MemoryLimitBytes: &memoryLimit,
	}}, providerType: "docker"}
	records := make(chan ExportRecord, 1)
	service, err := NewService(fixedResolver{target: target}, map[string]Source{"provider": source}, WithExporter(channelExporter{records: records}, ExportOptions{QueueCapacity: 1, Timeout: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }

	observation, err := service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusObserved {
		t.Fatalf("observation failed: %+v %v", observation, err)
	}
	cpuSeconds = 99
	memoryUsage = 99

	select {
	case record := <-records:
		if record.TenantID != "tenant" || record.SessionID != "session" || record.EnvironmentID != "environment" || record.AllocationID != "allocation" {
			t.Fatalf("exported identity mismatch: %+v", record)
		}
		if record.ProviderType != "docker" || record.Mode != ModeManaged || record.Status != StatusObserved || record.Reason != "" || record.CollectionSource != CollectionSourceOnRead {
			t.Fatalf("exported classification mismatch: %+v", record)
		}
		if record.Sample == nil || record.Sample.CPUUsageSecondsTotal == nil || *record.Sample.CPUUsageSecondsTotal != 12.5 || record.Sample.MemoryUsageBytes == nil || *record.Sample.MemoryUsageBytes != 1024 {
			t.Fatalf("exported sample was not independently copied: %+v", record.Sample)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Runtime observation export")
	}
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceMarksPeriodicHistoryCollection(t *testing.T) {
	now := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	startedAt := now.Add(-time.Minute)
	target := Target{
		TenantID: "tenant", SessionID: "session", EnvironmentID: "environment", Mode: ModeManaged,
		Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"},
	}
	records := make(chan ExportRecord, 1)
	service, err := NewService(
		fixedResolver{target: target},
		map[string]Source{"provider": &fixedSource{sample: Sample{ObservedAt: now, StartedAt: &startedAt}}},
		WithExporter(channelExporter{records: records}, ExportOptions{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	if _, err := service.ObserveSessionForHistory(t.Context(), "tenant", "session", samplerOwner{}, time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case record := <-records:
		if record.CollectionSource != CollectionSourcePeriodic {
			t.Fatalf("history collection source = %q", record.CollectionSource)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for periodic Runtime export")
	}
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceDoesNotExportPeriodicSampleAfterOwnershipLoss(t *testing.T) {
	now := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	startedAt := now.Add(-time.Minute)
	target := Target{
		TenantID: "tenant", SessionID: "session", EnvironmentID: "environment", Mode: ModeManaged,
		Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"},
	}
	records := make(chan ExportRecord, 1)
	service, err := NewService(
		fixedResolver{target: target},
		map[string]Source{"provider": &fixedSource{sample: Sample{ObservedAt: now, StartedAt: &startedAt}}},
		WithExporter(channelExporter{records: records}, ExportOptions{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	owner := &sequenceOwner{failAfter: 2}
	if _, err := service.ObserveSessionForHistory(t.Context(), "tenant", "session", owner, time.Second); err == nil {
		t.Fatal("periodic sample crossed a lost execution lease")
	}
	select {
	case record := <-records:
		t.Fatalf("lease-lost periodic sample reached exporter: %+v", record)
	default:
	}
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceRejectsUnsafeProviderTypeBeforeSamplingOrExport(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
	source := typedSource{fixedSource: &fixedSource{sample: Sample{ObservedAt: now}}, providerType: "docker native_id=secret"}
	records := make(chan ExportRecord, 1)
	service, err := NewService(
		fixedResolver{target: target},
		map[string]Source{"provider": source},
		WithExporter(channelExporter{records: records}, ExportOptions{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	if _, err := service.ObserveSession(t.Context(), "tenant", "session"); err == nil || source.calls != 0 {
		t.Fatalf("unsafe provider type reached sampling: err=%v calls=%d", err, source.calls)
	}
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case record := <-records:
		t.Fatalf("unsafe provider type reached exporter: %+v", record)
	default:
	}
}

func TestServiceExportQueueNeverBlocksOrChangesObservation(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
	exporter := &gatedExporter{started: make(chan struct{}, 1), release: make(chan struct{})}
	service, err := NewService(
		fixedResolver{target: target},
		map[string]Source{"provider": &fixedSource{sample: Sample{ObservedAt: now}}},
		WithExporter(exporter, ExportOptions{QueueCapacity: 1, Timeout: time.Second}),
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }

	if observation, err := service.ObserveSession(t.Context(), "tenant", "session"); err != nil || observation.Status != StatusObserved {
		t.Fatalf("first observation failed: %+v %v", observation, err)
	}
	select {
	case <-exporter.started:
	case <-time.After(time.Second):
		t.Fatal("exporter did not start")
	}
	for range 2 {
		completed := make(chan error, 1)
		go func() {
			observation, observeErr := service.ObserveSession(t.Context(), "tenant", "session")
			if observeErr == nil && observation.Status != StatusObserved {
				observeErr = errors.New("unexpected observation status")
			}
			completed <- observeErr
		}()
		select {
		case observeErr := <-completed:
			if observeErr != nil {
				t.Fatal(observeErr)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Runtime observation blocked on history exporter")
		}
	}
	close(exporter.release)
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls := exporter.callCount(); calls != 2 {
		t.Fatalf("export queue should retain one pending record and drop overflow, got %d calls", calls)
	}
}

func TestServiceIgnoresExporterFailureAndValidatesOptions(t *testing.T) {
	if _, err := NewService(fixedResolver{}, nil, WithExporter(nil, ExportOptions{})); err == nil {
		t.Fatal("nil exporter was accepted")
	}
	if _, err := NewService(fixedResolver{}, nil, WithExporter(channelExporter{}, ExportOptions{QueueCapacity: -1})); err == nil {
		t.Fatal("negative export queue capacity was accepted")
	}
	if _, err := NewService(fixedResolver{}, nil, WithExporter(channelExporter{}, ExportOptions{Timeout: -time.Second})); err == nil {
		t.Fatal("negative export timeout was accepted")
	}

	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	records := make(chan ExportRecord, 1)
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
	service, err := NewService(
		fixedResolver{target: target},
		map[string]Source{"provider": &fixedSource{sample: Sample{ObservedAt: now}}},
		WithExporter(channelExporter{records: records, err: errors.New("backend unavailable")}, ExportOptions{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	observation, err := service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusObserved {
		t.Fatalf("exporter failure changed observation: %+v %v", observation, err)
	}
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceCloseHonorsItsDeadlineWhenExporterDoesNot(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	exporter := stubbornExporter{started: make(chan struct{}), release: make(chan struct{})}
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
	service, err := NewService(
		fixedResolver{target: target},
		map[string]Source{"provider": &fixedSource{sample: Sample{ObservedAt: now}}},
		WithExporter(exporter, ExportOptions{QueueCapacity: 1, Timeout: time.Millisecond}),
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	if _, err := service.ObserveSession(t.Context(), "tenant", "session"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exporter.started:
	case <-time.After(time.Second):
		t.Fatal("exporter did not start")
	}

	closeCtx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := service.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close did not preserve its deadline: %v", err)
	}
	close(exporter.release)
}

func TestServiceIsolatesExporterPanics(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	exporter := panicExporter{called: make(chan struct{}, 1)}
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
	service, err := NewService(
		fixedResolver{target: target},
		map[string]Source{"provider": &fixedSource{sample: Sample{ObservedAt: now}}},
		WithExporter(exporter, ExportOptions{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	observation, err := service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusObserved {
		t.Fatalf("observation failed: %+v %v", observation, err)
	}
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exporter.called:
	default:
		t.Fatal("panicking exporter was not invoked")
	}
}

func TestServiceMapsOnlyDeclaredUnavailability(t *testing.T) {
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
	for _, tc := range []struct {
		err        error
		wantReason string
		wantError  bool
	}{
		{err: ErrUnavailable, wantReason: "sample_unavailable"},
		{err: ErrNotRunning, wantReason: "runtime_not_running"},
		{err: context.DeadlineExceeded, wantReason: "sample_timeout"},
		{err: errors.New("Docker permission denied"), wantError: true},
	} {
		service, err := NewService(fixedResolver{target: target}, map[string]Source{
			"provider": typedSource{fixedSource: &fixedSource{err: tc.err}, providerType: "docker"},
		})
		if err != nil {
			t.Fatal(err)
		}
		observation, err := service.ObserveSession(t.Context(), "tenant", "session")
		if (err != nil) != tc.wantError {
			t.Fatalf("wrong error classification: %+v %v", observation, err)
		}
		if !tc.wantError && (observation.Status != StatusUnavailable || observation.Reason != tc.wantReason || observation.ProviderType != "docker") {
			t.Fatalf("declared unavailability was not mapped: %+v", observation)
		}
	}
}

func TestServiceMapsAnActualSourceDeadlineWithoutLeakingIt(t *testing.T) {
	target := Target{
		EnvironmentID: "environment", Mode: ModeManaged,
		Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"},
	}
	service, err := NewService(fixedResolver{target: target}, map[string]Source{"provider": blockingSource{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	observation, err := service.ObserveSession(ctx, "tenant", "session")
	if err != nil || observation.Status != StatusUnavailable || observation.Reason != "sample_timeout" || observation.ProviderType != "docker" {
		t.Fatalf("source deadline was not safely classified: %+v %v", observation, err)
	}
}

func TestServiceExportsPeriodicSourceTimeoutAfterFinalOwnershipFence(t *testing.T) {
	target := Target{
		TenantID: "tenant", SessionID: "session", EnvironmentID: "environment", Mode: ModeManaged,
		Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"},
	}
	records := make(chan ExportRecord, 1)
	service, err := NewService(
		fixedResolver{target: target},
		map[string]Source{"provider": blockingSource{}},
		WithExporter(channelExporter{records: records}, ExportOptions{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	owner := &sequenceOwner{}
	observation, err := service.ObserveSessionForHistory(t.Context(), "tenant", "session", owner, 10*time.Millisecond)
	if err != nil || observation.Status != StatusUnavailable || observation.Reason != "sample_timeout" {
		t.Fatalf("periodic source timeout was not safely classified: %+v %v", observation, err)
	}
	if calls := owner.calls.Load(); calls != 2 {
		t.Fatalf("ownership checks = %d, want entry and pre-export fences", calls)
	}
	select {
	case record := <-records:
		if record.Status != StatusUnavailable || record.Reason != "sample_timeout" || record.CollectionSource != CollectionSourcePeriodic {
			t.Fatalf("periodic timeout export mismatch: %+v", record)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for periodic timeout export")
	}
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceClassifiesResolverAndTerminalAllocationUnavailability(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	service, err := NewService(fixedResolver{target: Target{SessionID: "session", EnvironmentID: "environment", Mode: ModeManaged}, err: ErrUnavailable}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	observation, err := service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusUnavailable || observation.Reason != "allocation_pending" || !observation.ResolvedAt.Equal(now) {
		t.Fatalf("pending allocation was not classified: %+v %v", observation, err)
	}

	source := &fixedSource{}
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "creating"}}
	service, err = NewService(fixedResolver{target: target}, map[string]Source{"provider": source})
	if err != nil {
		t.Fatal(err)
	}
	observation, err = service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusUnavailable || observation.Reason != "allocation_pending" || source.calls != 0 {
		t.Fatalf("creating allocation reached its provider: %+v %v calls=%d", observation, err, source.calls)
	}

	target = Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "released"}}
	service, err = NewService(fixedResolver{target: target}, map[string]Source{"provider": &fixedSource{}})
	if err != nil {
		t.Fatal(err)
	}
	observation, err = service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusUnavailable || observation.Reason != "runtime_not_running" {
		t.Fatalf("released allocation was not classified: %+v %v", observation, err)
	}

	source = &fixedSource{}
	target = Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{
		AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running", AllocationCreatedAt: time.Now().Add(time.Hour),
	}}
	service, err = NewService(fixedResolver{target: target}, map[string]Source{"provider": source})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ObserveSession(t.Context(), "tenant", "session"); err == nil || source.calls != 0 {
		t.Fatalf("future allocation creation reached its provider: %v calls=%d", err, source.calls)
	}
}

func TestServiceRejectsUnsafeProviderSamples(t *testing.T) {
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	preEpoch := time.Unix(-1, 0).UTC()
	tooLarge := uint64(1 << 53)
	for _, sample := range []Sample{
		{ObservedAt: preEpoch},
		{ObservedAt: now, StartedAt: &preEpoch},
		{ObservedAt: now, CPUUsageSecondsTotal: float64Pointer(-1)},
		{ObservedAt: now, CPUUsageSecondsTotal: float64Pointer(math.NaN())},
		{ObservedAt: now, CPUUsageSecondsTotal: float64Pointer(math.Inf(1))},
		{ObservedAt: now, CPUCapacityCores: float64Pointer(0)},
		{ObservedAt: now, MemoryUsageBytes: &tooLarge},
		{ObservedAt: now, MemoryLimitBytes: &tooLarge},
	} {
		service, err := NewService(fixedResolver{target: target}, map[string]Source{"provider": &fixedSource{sample: sample}})
		if err != nil {
			t.Fatal(err)
		}
		service.now = func() time.Time { return now }
		if _, err := service.ObserveSession(t.Context(), "tenant", "session"); err == nil {
			t.Fatalf("unsafe sample accepted: %+v", sample)
		}
	}
}

func TestServiceRejectsMismatchedResolvedOwnership(t *testing.T) {
	for _, target := range []Target{
		{TenantID: "other", SessionID: "session", Mode: ModeNone},
		{TenantID: "tenant", SessionID: "other", Mode: ModeNone},
		{TenantID: "tenant", SessionID: "session", EnvironmentID: "unexpected", Mode: ModeNone},
		{TenantID: "tenant", SessionID: "session", Mode: ModeSelfHosted},
	} {
		service, err := NewService(fixedResolver{target: target}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.ObserveSession(t.Context(), "tenant", "session"); err == nil {
			t.Fatalf("mismatched ownership accepted: %+v", target)
		}
	}
}

func float64Pointer(value float64) *float64 { return &value }
