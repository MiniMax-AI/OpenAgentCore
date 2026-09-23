package docker

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/google/uuid"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

func TestObserveVerifiesOwnershipThenReadsOneShotStats(t *testing.T) {
	installationID := uuid.NewString()
	target := runtimeobs.Target{
		TenantID: uuid.NewString(), SessionID: uuid.NewString(), EnvironmentID: uuid.NewString(), Mode: runtimeobs.ModeManaged,
		Instance: runtimeobs.Instance{AllocationID: uuid.NewString(), ProviderKey: installationID, DeviceID: uuid.NewString()},
	}
	observed := time.Now().UTC().Truncate(time.Microsecond)
	started := observed.Add(-time.Minute)
	statsRead := false
	omitMeasurements := false
	running := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/json"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id":         "container-id",
				"State":      map[string]any{"Status": "running", "Running": running, "StartedAt": started.Format(time.RFC3339Nano)},
				"HostConfig": map[string]any{"NanoCpus": 2_000_000_000, "Memory": 2048},
				"Config": map[string]any{"Labels": map[string]string{
					labelPrefix + "installation": installationID,
					labelPrefix + "tenant":       target.TenantID,
					labelPrefix + "environment":  target.EnvironmentID,
					labelPrefix + "allocation":   target.Instance.AllocationID,
				}},
			})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/containers/container-id/stats"):
			if r.URL.Query().Get("stream") != "false" || r.URL.Query().Get("one-shot") != "true" {
				t.Errorf("stats request was not one-shot: %s", r.URL.RawQuery)
			}
			statsRead = true
			if omitMeasurements {
				_ = json.NewEncoder(w).Encode(map[string]any{"read": observed})
				return
			}
			_ = json.NewEncoder(w).Encode(container.StatsResponse{
				Read:        observed,
				CPUStats:    container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 1_500_000_000}},
				MemoryStats: container.MemoryStats{Usage: 1024},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c, err := client.New(client.WithHost(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	p, err := New(c, Config{InstallationID: installationID, Image: "fixture@sha256:" + strings.Repeat("a", 64), Network: "bridge", Seccomp: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	sample, err := p.Observe(t.Context(), target)
	if err != nil || !statsRead || sample.CPUUsageSecondsTotal == nil || *sample.CPUUsageSecondsTotal != 1.5 || sample.MemoryUsageBytes == nil || *sample.MemoryUsageBytes != 1024 {
		t.Fatalf("bad one-shot observation: %+v %v stats=%v", sample, err, statsRead)
	}
	omitMeasurements = true
	missing, err := p.Observe(t.Context(), target)
	if err != nil || missing.CPUUsageSecondsTotal != nil || missing.MemoryUsageBytes != nil || missing.CPUCapacityCores == nil || missing.MemoryLimitBytes == nil {
		t.Fatalf("missing Docker measurements became zero: %+v %v", missing, err)
	}
	running = false
	statsRead = false
	if _, err := p.Observe(t.Context(), target); !errors.Is(err, runtimeobs.ErrNotRunning) || statsRead {
		t.Fatalf("stopped Runtime was not classified before stats: %v stats=%v", err, statsRead)
	}
	running = true
	foreign := target
	foreign.Instance.ProviderKey = uuid.NewString()
	statsRead = false
	if _, err := p.Observe(t.Context(), foreign); err == nil || statsRead {
		t.Fatal("foreign provider identity reached Docker stats")
	}
}

func TestSampleFromDockerPreservesObservedZeroAndConfiguredCapacity(t *testing.T) {
	observed := time.Date(2026, 9, 22, 1, 2, 3, 0, time.UTC)
	started := observed.Add(-5 * time.Minute)
	zero := uint64(0)
	sample, err := sampleFromDocker(container.InspectResponse{
		State:      &container.State{Running: true, StartedAt: started.Format(time.RFC3339Nano)},
		HostConfig: &container.HostConfig{Resources: container.Resources{NanoCPUs: 2_000_000_000, Memory: 2 * 1024 * 1024 * 1024}},
	}, testDockerStats(observed, &zero, &zero))
	if err != nil {
		t.Fatal(err)
	}
	if sample.ObservedAt != observed || sample.StartedAt == nil || !sample.StartedAt.Equal(started) {
		t.Fatalf("lost Docker observation time: %+v", sample)
	}
	if sample.CPUUsageSecondsTotal == nil || *sample.CPUUsageSecondsTotal != 0 || sample.MemoryUsageBytes == nil || *sample.MemoryUsageBytes != 0 {
		t.Fatalf("observed zero became unavailable: %+v", sample)
	}
	if sample.CPUCapacityCores == nil || *sample.CPUCapacityCores != 2 || sample.MemoryLimitBytes == nil || *sample.MemoryLimitBytes != 2*1024*1024*1024 {
		t.Fatalf("lost configured capacity: %+v", sample)
	}
}

func TestSampleFromDockerNormalizesCumulativeCPU(t *testing.T) {
	observed := time.Now().UTC()
	started := observed.Add(-time.Hour)
	cpu, memory := uint64(2_500_000_000), uint64(4096)
	sample, err := sampleFromDocker(container.InspectResponse{
		State:      &container.State{Running: true, StartedAt: started.Format(time.RFC3339Nano)},
		HostConfig: &container.HostConfig{},
	}, testDockerStats(observed, &cpu, &memory))
	if err != nil {
		t.Fatal(err)
	}
	if sample.CPUUsageSecondsTotal == nil || *sample.CPUUsageSecondsTotal != 2.5 || sample.MemoryUsageBytes == nil || *sample.MemoryUsageBytes != 4096 {
		t.Fatalf("bad Docker normalization: %+v", sample)
	}
	if sample.CPUCapacityCores != nil || sample.MemoryLimitBytes != nil {
		t.Fatalf("invented unconfigured capacity: %+v", sample)
	}
}

func TestSampleFromDockerRejectsIncompleteState(t *testing.T) {
	observed := time.Now().UTC()
	for _, inspected := range []container.InspectResponse{
		{},
		{State: &container.State{Running: false}, HostConfig: &container.HostConfig{}},
		{State: &container.State{Running: true, StartedAt: "invalid"}, HostConfig: &container.HostConfig{}},
	} {
		if _, err := sampleFromDocker(inspected, dockerStatsResponse{Read: observed}); err == nil {
			t.Fatal("accepted incomplete Docker state")
		}
	}
}

func testDockerStats(observed time.Time, cpu, memory *uint64) dockerStatsResponse {
	stats := dockerStatsResponse{Read: observed}
	if cpu != nil {
		stats.CPUStats = &struct {
			CPUUsage *struct {
				TotalUsage *uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
		}{CPUUsage: &struct {
			TotalUsage *uint64 `json:"total_usage"`
		}{TotalUsage: cpu}}
	}
	if memory != nil {
		stats.MemoryStats = &struct {
			Usage *uint64 `json:"usage"`
		}{Usage: memory}
	}
	return stats
}
