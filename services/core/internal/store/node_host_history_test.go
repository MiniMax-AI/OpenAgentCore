package store

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

func hostPtr[T any](value T) *T { return &value }

func TestNodeHostHistorySamplingAndDetail(t *testing.T) {
	s, _, d := managerFixture(t, 2, 8)
	connection := onlineManagerNode(t, s, d.LocalNodeID)
	epoch := managerEpoch(t, s)
	now := time.Now().UTC()
	host := &RuntimeNodeHost{EffectiveCPUCores: hostPtr(2.0), CPUUtilization: hostPtr(0.35), TotalMemoryBytes: hostPtr(int64(4096)), AvailableMemoryBytes: hostPtr(int64(1024)), AvailableDiskBytes: hostPtr(int64(8192)), ObservedAt: &now}
	health := RuntimeNodeHealth{ProviderReady: true, Host: host}
	if err := s.HeartbeatRuntimeNode(t.Context(), d.LocalNodeID, connection, epoch, health); err != nil {
		t.Fatal(err)
	}
	for i, want := range []int64{1, 0} {
		if n, err := s.SampleNodeHostHistory(t.Context()); err != nil || n != want {
			t.Fatal(i, n, err)
		}
	}
	detail, err := s.GetRuntimeNodeDetail(t.Context(), d.LocalNodeID, "1h")
	if err != nil || detail.Host.TotalMemoryBytes == nil || *detail.Host.CPUUtilization != 0.35 || len(detail.History.Points) != 60 {
		t.Fatal(detail, err)
	}
	// The current partial minute does not enter history yet.
	for _, p := range detail.History.Points {
		if p.CPUUtilizationMax != nil {
			t.Fatal("partial bucket leaked", p)
		}
	}
	list, err := s.ListRuntimeNodes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(list[0])
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for _, key := range []string{"host", "history", "cpu_utilization", "total_memory_bytes", "effective_cpu_cores"} {
		if _, exists := fields[key]; exists {
			t.Fatal("list contract expanded", key, string(raw))
		}
	}
	// Sample maxima/minima differ from the latest reading.
	end := time.Now().UTC().Truncate(time.Minute)
	start := end.Add(-time.Minute)
	for _, sample := range []struct {
		at   time.Time
		cpu  any
		mem  any
		disk any
	}{
		{start.Add(time.Second), 0.1, int64(100), int64(800)},
		{start.Add(20 * time.Second), 0.8, int64(70), int64(900)},
		{start.Add(40 * time.Second), nil, int64(500), int64(600)},
		{start.Add(-time.Minute), nil, nil, nil},
	} {
		if _, err := s.pool.Exec(t.Context(), "INSERT INTO node_host_history_samples(node_id, observed_at,cpu_utilization,memory_used_bytes,available_disk_bytes) VALUES($1,$2,$3,$4,$5)", d.LocalNodeID, sample.at, sample.cpu, sample.mem, sample.disk); err != nil {
			t.Fatal(err)
		}
	}
	detail, err = s.GetRuntimeNodeDetail(t.Context(), d.LocalNodeID, "1h")
	if err != nil {
		t.Fatal(err)
	}
	last := detail.History.Points[len(detail.History.Points)-1]
	if !last.Start.Equal(start) || last.CPUUtilizationMax == nil || *last.CPUUtilizationMax != 0.8 || *last.MemoryUsedBytesMax != 500 || *last.AvailableDiskBytesMin != 600 {
		t.Fatal(last)
	}
	previous := detail.History.Points[len(detail.History.Points)-2]
	if previous.CPUUtilizationMax != nil || previous.MemoryUsedBytesMax != nil || previous.AvailableDiskBytesMin != nil {
		t.Fatal(previous)
	}
	for name, want := range map[string]int{"6h": 72, "24h": 96} {
		detail, err := s.GetRuntimeNodeDetail(t.Context(), d.LocalNodeID, name)
		if err != nil || len(detail.History.Points) != want {
			t.Fatal(name, detail, err)
		}
	}
	if _, err := s.GetRuntimeNodeDetail(t.Context(), uuid.NewString(), "1h"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	for _, name := range []string{"", "7d", "other"} {
		if _, err := s.GetRuntimeNodeDetail(t.Context(), d.LocalNodeID, name); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	// A disconnected node cannot create another history row, even with a fresh last observation.
	next := now.Add(time.Millisecond)
	host.ObservedAt = &next
	if err := s.HeartbeatRuntimeNode(t.Context(), d.LocalNodeID, connection, epoch, health); err != nil {
		t.Fatal(err)
	}
	if err := s.DisconnectRuntimeNode(t.Context(), d.LocalNodeID, connection, epoch); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SampleNodeHostHistory(t.Context()); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	// A restarted service can query existing durable history without re-sampling.
	fresh := New(s.pool)
	restored, err := fresh.GetRuntimeNodeDetail(t.Context(), d.LocalNodeID, "1h")
	if err != nil || restored.Online || restored.History.Points[59].CPUUtilizationMax == nil {
		t.Fatal(restored, err)
	}
	if !restored.Host.ObservedAt.Equal(next) {
		t.Fatal(restored.Host)
	}
	// Retention uses the same existing cleanup operation and does not delete nodes.
	old := now.Add(-8 * 24 * time.Hour)
	if _, err := s.pool.Exec(t.Context(), "INSERT INTO node_host_history_samples(node_id, observed_at) VALUES($1,$2)", d.LocalNodeID, old); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneRuntimeHistorySamples(t.Context(), now.Add(-7*24*time.Hour).UnixNano()); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := s.GetRuntimeNodeDetail(t.Context(), d.LocalNodeID, "1h"); err != nil {
		t.Fatal(err)
	}
}

func TestNodeHostHistoryFencingAndUnknown(t *testing.T) {
	s, _, d := managerFixture(t, 2, 8)
	conn := onlineManagerNode(t, s, d.LocalNodeID)
	epoch := managerEpoch(t, s)
	for _, at := range []time.Time{time.Now().Add(-time.Minute), time.Now().Add(time.Hour)} {
		if err := s.HeartbeatRuntimeNode(t.Context(), d.LocalNodeID, conn, epoch, RuntimeNodeHealth{Host: &RuntimeNodeHost{ObservedAt: &at}}); err != nil {
			t.Fatal(err)
		}
		if n, err := s.SampleNodeHostHistory(t.Context()); err != nil || n != 0 {
			t.Fatal(n, err)
		}
	}
	now := time.Now().UTC()
	health := RuntimeNodeHealth{Host: &RuntimeNodeHost{ObservedAt: &now}}
	if err := s.HeartbeatRuntimeNode(t.Context(), d.LocalNodeID, uuid.NewString(), epoch, health); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal(err)
	}
	if err := s.HeartbeatRuntimeNode(t.Context(), d.LocalNodeID, conn, epoch, health); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SampleNodeHostHistory(t.Context()); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	var cpu *float64
	var memory, disk *int64
	if err := s.pool.QueryRow(t.Context(), "SELECT cpu_utilization,memory_used_bytes,available_disk_bytes FROM node_host_history_samples WHERE node_id=$1", d.LocalNodeID).Scan(&cpu, &memory, &disk); err != nil || cpu != nil || memory != nil || disk != nil {
		t.Fatal(cpu, memory, disk, err)
	}
	for _, host := range []*RuntimeNodeHost{
		{ObservedAt: &now, CPUUtilization: hostPtr(1.1)}, {ObservedAt: &now, CPUUtilization: hostPtr(math.NaN())},
		{ObservedAt: &now, TotalMemoryBytes: hostPtr(int64(10)), AvailableMemoryBytes: hostPtr(int64(11))},
		{ObservedAt: &now, AvailableDiskBytes: hostPtr(int64(-1))}, {ObservedAt: &now, EffectiveCPUCores: hostPtr(0.0)}, {},
	} {
		if err := s.HeartbeatRuntimeNode(t.Context(), d.LocalNodeID, conn, epoch, RuntimeNodeHealth{Host: host}); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(host, err)
		}
	}
}
