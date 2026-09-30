package deploymentpg_test

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func hostHistoryPtr[T any](value T) *T { return &value }

// hostHistoryNode initializes a Docker deployment and connects one enrolled
// node, returning it with its connection and the owner epoch.
func hostHistoryNode(t *testing.T) (fixture, string, string, uint64) {
	t.Helper()
	f := newFixture(t)
	changes, _ := f.execution(t)
	_, view := f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 2, MaxRetained: 8})
	connection := f.connect(t, node.NodeID)
	epoch, err := f.adapter.OwnerEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return f, node.NodeID, connection, epoch
}

func TestNodeHostHistorySamplingAndDetail(t *testing.T) {
	f, node, connection, epoch := hostHistoryNode(t)
	now := time.Now().UTC()
	host := &deployment.NodeHost{EffectiveCPUCores: hostHistoryPtr(2.0), CPUUtilization: hostHistoryPtr(0.35), TotalMemoryBytes: hostHistoryPtr(int64(4096)), AvailableMemoryBytes: hostHistoryPtr(int64(1024)), AvailableDiskBytes: hostHistoryPtr(int64(8192)), ObservedAt: &now}
	health := deployment.NodeHealth{ProviderReady: true, Host: host}
	if err := f.service.Heartbeat(t.Context(), node, connection, epoch, health); err != nil {
		t.Fatal(err)
	}
	for i, want := range []int64{1, 0} {
		if n, err := f.adapter.SampleHostHistory(t.Context()); err != nil || n != want {
			t.Fatal(i, n, err)
		}
	}
	detail, err := f.service.NodeDetail(t.Context(), node, "1h")
	if err != nil || detail.Host.TotalMemoryBytes == nil || *detail.Host.CPUUtilization != 0.35 || len(detail.History.Points) != 60 {
		t.Fatal(detail, err)
	}
	// The current partial minute does not enter history yet.
	for _, p := range detail.History.Points {
		if p.CPUUtilizationMax != nil {
			t.Fatal("partial bucket leaked", p)
		}
	}
	list, err := f.service.ListNodes(t.Context())
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
		if _, err := f.pool.Exec(t.Context(), "INSERT INTO node_host_history_samples(node_id, observed_at,cpu_utilization,memory_used_bytes,available_disk_bytes) VALUES($1,$2,$3,$4,$5)", node, sample.at, sample.cpu, sample.mem, sample.disk); err != nil {
			t.Fatal(err)
		}
	}
	detail, err = f.service.NodeDetail(t.Context(), node, "1h")
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
		detail, err := f.service.NodeDetail(t.Context(), node, name)
		if err != nil || len(detail.History.Points) != want {
			t.Fatal(name, detail, err)
		}
	}
	if _, err := f.service.NodeDetail(t.Context(), uuid.NewString(), "1h"); !errors.Is(err, deployment.ErrNotFound) {
		t.Fatal(err)
	}
	for _, name := range []string{"", "7d", "other"} {
		if _, err := f.service.NodeDetail(t.Context(), node, name); !errors.Is(err, deployment.ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	// A disconnected node cannot create another history row, even with a fresh last observation.
	next := now.Add(time.Millisecond)
	host.ObservedAt = &next
	if err := f.service.Heartbeat(t.Context(), node, connection, epoch, health); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DisconnectNode(t.Context(), node, connection, epoch); err != nil {
		t.Fatal(err)
	}
	if n, err := f.adapter.SampleHostHistory(t.Context()); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	// A restarted service can query existing durable history without re-sampling.
	_, fresh := f.withPublicURL(t, fixturePublicURL)
	restored, err := fresh.NodeDetail(t.Context(), node, "1h")
	if err != nil || restored.Online || restored.History.Points[59].CPUUtilizationMax == nil {
		t.Fatal(restored, err)
	}
	if !restored.Host.ObservedAt.Equal(next) {
		t.Fatal(restored.Host)
	}
}

func TestNodeHostHistoryFencingAndUnknown(t *testing.T) {
	f, node, conn, epoch := hostHistoryNode(t)
	for _, at := range []time.Time{time.Now().Add(-time.Minute), time.Now().Add(time.Hour)} {
		if err := f.service.Heartbeat(t.Context(), node, conn, epoch, deployment.NodeHealth{Host: &deployment.NodeHost{ObservedAt: &at}}); err != nil {
			t.Fatal(err)
		}
		if n, err := f.adapter.SampleHostHistory(t.Context()); err != nil || n != 0 {
			t.Fatal(n, err)
		}
	}
	now := time.Now().UTC()
	health := deployment.NodeHealth{Host: &deployment.NodeHost{ObservedAt: &now}}
	if err := f.service.Heartbeat(t.Context(), node, uuid.NewString(), epoch, health); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal(err)
	}
	if err := f.service.Heartbeat(t.Context(), node, conn, epoch, health); err != nil {
		t.Fatal(err)
	}
	if n, err := f.adapter.SampleHostHistory(t.Context()); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	var cpu *float64
	var memory, disk *int64
	if err := f.pool.QueryRow(t.Context(), "SELECT cpu_utilization,memory_used_bytes,available_disk_bytes FROM node_host_history_samples WHERE node_id=$1", node).Scan(&cpu, &memory, &disk); err != nil || cpu != nil || memory != nil || disk != nil {
		t.Fatal(cpu, memory, disk, err)
	}
	for _, host := range []*deployment.NodeHost{
		{ObservedAt: &now, CPUUtilization: hostHistoryPtr(1.1)}, {ObservedAt: &now, CPUUtilization: hostHistoryPtr(math.NaN())},
		{ObservedAt: &now, TotalMemoryBytes: hostHistoryPtr(int64(10)), AvailableMemoryBytes: hostHistoryPtr(int64(11))},
		{ObservedAt: &now, AvailableDiskBytes: hostHistoryPtr(int64(-1))}, {ObservedAt: &now, EffectiveCPUCores: hostHistoryPtr(0.0)}, {},
	} {
		if err := f.service.Heartbeat(t.Context(), node, conn, epoch, deployment.NodeHealth{Host: host}); !errors.Is(err, deployment.ErrInvalidInput) {
			t.Fatal(host, err)
		}
	}
}
