package runtimeobs

import (
	"testing"
	"time"
)

func TestExporterOutageDoesNotDelayOtherDestinations(t *testing.T) {
	now := time.Now()
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
	blocked := &gatedExporter{started: make(chan struct{}, 1), release: make(chan struct{})}
	records := make(chan ExportRecord, 1)
	service, err := NewService(fixedResolver{target: target}, map[string]Source{"provider": &fixedSource{sample: Sample{ObservedAt: now}}},
		WithExporter(blocked, ExportOptions{QueueCapacity: 1, Timeout: time.Second}),
		WithExporter(channelExporter{records: records}, ExportOptions{QueueCapacity: 1, Timeout: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { close(blocked.release); _ = service.Close(t.Context()) }()
	if _, err := service.ObserveSession(t.Context(), "tenant", "session"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked.started:
	case <-time.After(time.Second):
		t.Fatal("blocked exporter never started")
	}
	select {
	case <-records:
	case <-time.After(time.Second):
		t.Fatal("local history waited for external exporter")
	}
}
