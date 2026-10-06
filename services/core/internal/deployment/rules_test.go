package deployment

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestValidateCoreURL(t *testing.T) {
	for _, value := range []string{"https://core.example", "https://core.example:8443", "http://localhost:8091", "http://127.0.0.2:8091", "http://[::1]:8091", "https://[2001:db8::1]", "http://core.example", "http://core:8091", "http://10.0.0.5:8080"} {
		if err := ValidateCoreURL(value); err != nil {
			t.Errorf("valid Core URL %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{
		"", "ftp://core.example", "ws://core.example", "https://core.example/", "https://user:secret@core.example",
		"https://core.example/path", "https://core.example?", "https://core.example?q=x", "https://core.example#x", "https://core.example#",
		"https://CORE.example", "https://core.example:", "https://core.example:0", "https://core.example:65536", "https://core.example:0080",
		"https://core.example\\evil", "https://[not-an-ip]", "https://-core.example", "https://core..example", "https://core_example",
		"https://core.example.", "https://bücher.example", "https://core.example:0443",
	} {
		if err := ValidateCoreURL(value); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("invalid Core URL %q accepted: %v", value, err)
		}
	}
}

func TestParseID(t *testing.T) {
	id := uuid.New()
	if got, err := parseID(strings.ToUpper(id.String())); err != nil || got != id.String() {
		t.Fatalf("parseID returned %q, %v; want the canonical %q", got, err, id)
	}
	for _, value := range []string{"", "not-a-uuid", uuid.Nil.String()} {
		if _, err := parseID(value); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("parseID(%q) = %v, want ErrInvalidInput", value, err)
		}
	}
}

func TestDigestAndGenerationRules(t *testing.T) {
	digest := tokenDigest("token")
	if !validDigest(digest) {
		t.Fatal("tokenDigest is not a valid digest")
	}
	for _, value := range []string{"", strings.ToUpper(digest), digest[:63], digest + "0", strings.Repeat("g", 64)} {
		if validDigest(value) {
			t.Errorf("validDigest(%q) accepted", value)
		}
	}
	for generation, want := range map[uint64]bool{0: false, 1: true, math.MaxInt64: true, math.MaxInt64 + 1: false} {
		if validGeneration(generation) != want {
			t.Errorf("validGeneration(%d) != %v", generation, want)
		}
	}
	for credential, want := range map[string]bool{
		strings.Repeat("n", 31): false, strings.Repeat("n", 32): true, strings.Repeat("n", 256): true, strings.Repeat("n", 257): false,
		strings.Repeat("n", 32) + " ": false, strings.Repeat("n", 32) + "\t": false, strings.Repeat("n", 32) + "\n": false,
	} {
		if validNodeCredential(credential) != want {
			t.Errorf("validNodeCredential(%d characters %q) != %v", len(credential), credential[len(credential)-1:], want)
		}
	}
}

func TestValidateNode(t *testing.T) {
	for _, c := range []struct {
		name             string
		active, retained int
		code, param      string
	}{
		{"node", 1, 1, "", ""},
		{strings.Repeat("n", 128), 1000000, 1000000, "", ""},
		// Node names keep their byte bound and limited control set; they do not
		// use the stricter Project and key name validator.
		{"node\tname", 1, 1000000, "", ""},
		{string([]byte{0xff}), 1, 1000000, "", ""},
		{"", 1, 1, "invalid_name", "name"},
		{"", 0, 0, "invalid_name", "name"},
		{strings.Repeat("界", 43), 1, 1, "invalid_name", "name"},
		{"  ", 1, 1, "invalid_name", "name"},
		{strings.Repeat("n", 129), 1, 1, "invalid_name", "name"},
		{"a\nb", 1, 1, "invalid_name", "name"},
		{"a\rb", 1, 1, "invalid_name", "name"},
		{"a\x00b", 1, 1, "invalid_name", "name"},
		{"node", 0, 1, "invalid_node_capacity", "max_active"},
		{"node", 0, 0, "invalid_node_capacity", "max_active"},
		{"node", 1000001, 1000001, "invalid_node_capacity", "max_active"},
		{"node", 1000001, 8, "invalid_node_capacity", "max_active"},
		{"node", 2, 1, "invalid_node_capacity", "max_retained"},
		{"node", 1, 1000001, "invalid_node_capacity", "max_retained"},
	} {
		err := validateNode(c.name, c.active, c.retained)
		if c.code == "" {
			if err != nil {
				t.Errorf("validateNode(%q, %d, %d) = %v", c.name, c.active, c.retained, err)
			}
			continue
		}
		var validation *NodeValidationError
		if !errors.As(err, &validation) || !errors.Is(err, ErrInvalidInput) || validation.Code != c.code || validation.Param != c.param || err.Error() != ErrInvalidInput.Error() {
			t.Errorf("validateNode(%q, %d, %d) = %#v, want %s on %s", c.name, c.active, c.retained, err, c.code, c.param)
		}
		if c.code == "invalid_name" && validation != nil && validation.MaxLength != 128 {
			t.Errorf("name rejection reports max length %d", validation.MaxLength)
		}
	}
}

func TestCheckGeneration(t *testing.T) {
	installation := uuid.NewString()
	current := Record{InstallationID: installation, WebManaged: true, Generation: 3}
	if err := checkGeneration(current, installation, 3); err != nil {
		t.Fatal(err)
	}
	var stale *GenerationStaleError
	if err := checkGeneration(current, installation, 2); !errors.As(err, &stale) || stale.CurrentGeneration != 3 || !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation: %v", err)
	}
	process := current
	process.WebManaged = false
	for _, c := range []struct {
		d            Record
		installation string
	}{{process, installation}, {current, uuid.NewString()}} {
		if err := checkGeneration(c.d, c.installation, 3); !errors.Is(err, ErrConflict) || errors.As(err, &stale) {
			t.Errorf("checkGeneration(%+v, %s) = %v, want a plain conflict", c.d, c.installation, err)
		}
	}
	if initialized(Record{InstallationID: installation}) || initialized(Record{Provider: "docker"}) || !initialized(Record{InstallationID: installation, Provider: "docker"}) {
		t.Fatal("initialized needs both an installation and a provider")
	}
}

func TestTypedErrors(t *testing.T) {
	if err := error(&ResetRequiredError{CurrentProvider: "docker", RequestedProvider: "e2b"}); !errors.Is(err, ErrConflict) {
		t.Fatal("ResetRequiredError is not a conflict")
	}
	validation := &sandbox.ValidationError{Param: "resources.cpus", Message: "too many"}
	err := configurationError(validation)
	if !errors.Is(err, ErrInvalidInput) || err.Validation != validation || err.Message != "too many" {
		t.Fatalf("configurationError = %#v", err)
	}
	if plain := configurationError(errors.New("rejected")); plain.Validation != nil || plain.Error() != "rejected" {
		t.Fatalf("configurationError of a plain error = %#v", plain)
	}
	if got := string(configurationJSON(nil)); got != "{}" {
		t.Fatalf("configurationJSON(nil) = %s", got)
	}
	if got := string(configurationJSON([]byte(`{"a":1}`))); got != `{"a":1}` {
		t.Fatalf("configurationJSON kept %s", got)
	}
}

func TestNormalizeHealth(t *testing.T) {
	negative, zero, one := int64(-1), float64(0), float64(1)
	now := time.Now()
	for _, c := range []struct {
		name       string
		in         NodeHealth
		diagnostic string
		invalid    bool
	}{
		{"ready clears the diagnostic", NodeHealth{ProviderReady: true, Diagnostic: sandbox.NodeProviderUnavailable}, "", false},
		{"unknown text becomes provider_unavailable", NodeHealth{Diagnostic: "disk on fire"}, sandbox.NodeProviderUnavailable, false},
		{"empty stays empty", NodeHealth{}, "", false},
		{"negative CPU count", NodeHealth{CPUCount: &negative}, "", true},
		{"negative memory", NodeHealth{AvailableMemoryBytes: &negative}, "", true},
		{"negative disk", NodeHealth{AvailableDiskBytes: &negative}, "", true},
		{"host without observation", NodeHealth{Host: &NodeHost{CPUUtilization: &one}}, "", true},
		{"valid host", NodeHealth{Host: &NodeHost{ObservedAt: &now, CPUUtilization: &one, EffectiveCPUCores: &one}}, "", false},
		{"zero effective cores", NodeHealth{Host: &NodeHost{ObservedAt: &now, EffectiveCPUCores: &zero}}, "", true},
	} {
		got, err := normalizeHealth(c.in)
		if c.invalid != errors.Is(err, ErrInvalidInput) || (!c.invalid && (err != nil || got.Diagnostic != c.diagnostic)) {
			t.Errorf("%s: normalizeHealth = %+v, %v", c.name, got, err)
		}
	}
}

func TestValidateHost(t *testing.T) {
	now := time.Now()
	early, late := time.Date(1969, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	half, over, negative, nan, inf := 0.5, 1.5, -0.5, math.NaN(), math.Inf(1)
	total, more, tooLarge, negativeBytes, zeroBytes := int64(100), int64(101), int64(1<<53), int64(-1), int64(0)
	for _, c := range []struct {
		name string
		host *NodeHost
		ok   bool
	}{
		{"absent", nil, true},
		{"complete", &NodeHost{ObservedAt: &now, CPUUtilization: &half, EffectiveCPUCores: &half, TotalMemoryBytes: &total, AvailableMemoryBytes: &total, AvailableDiskBytes: &total}, true},
		{"no observation time", &NodeHost{}, false},
		{"zero observation time", &NodeHost{ObservedAt: &time.Time{}}, false},
		{"before 1970", &NodeHost{ObservedAt: &early}, false},
		{"after 9999", &NodeHost{ObservedAt: &late}, false},
		{"utilization above 1", &NodeHost{ObservedAt: &now, CPUUtilization: &over}, false},
		{"negative utilization", &NodeHost{ObservedAt: &now, CPUUtilization: &negative}, false},
		{"NaN utilization", &NodeHost{ObservedAt: &now, CPUUtilization: &nan}, false},
		{"infinite cores", &NodeHost{ObservedAt: &now, EffectiveCPUCores: &inf}, false},
		{"negative bytes", &NodeHost{ObservedAt: &now, AvailableDiskBytes: &negativeBytes}, false},
		{"bytes beyond 2^53-1", &NodeHost{ObservedAt: &now, AvailableDiskBytes: &tooLarge}, false},
		{"zero total memory", &NodeHost{ObservedAt: &now, TotalMemoryBytes: &zeroBytes}, false},
		{"available above total", &NodeHost{ObservedAt: &now, TotalMemoryBytes: &total, AvailableMemoryBytes: &more}, false},
	} {
		if err := validateHost(c.host); c.ok != (err == nil) || (!c.ok && !errors.Is(err, ErrInvalidInput)) {
			t.Errorf("%s: validateHost = %v", c.name, err)
		}
	}
}

func TestHistoryPoints(t *testing.T) {
	start := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	window := coremetrics.Range{Start: start, End: start.Add(3 * time.Minute), ResolutionSeconds: 60}
	used := int64(7)
	// A sample in another zone still fills its UTC bucket.
	sample := HostHistoryPoint{Start: start.Add(time.Minute).In(time.FixedZone("east", 8*3600)), MemoryUsedBytesMax: &used}
	points := historyPoints(window, []HostHistoryPoint{sample})
	if len(points) != 3 {
		t.Fatalf("got %d points, want one per bucket", len(points))
	}
	for i, p := range points {
		if !p.Start.Equal(start.Add(time.Duration(i) * time.Minute)) {
			t.Errorf("point %d starts at %v", i, p.Start)
		}
		if filled := p.MemoryUsedBytesMax != nil; filled != (i == 1) || p.CPUUtilizationMax != nil || p.AvailableDiskBytesMin != nil {
			t.Errorf("point %d = %+v", i, p)
		}
	}
	if points[1].Start.Location() != time.UTC {
		t.Fatal("sample bucket is not UTC")
	}
	if got := historyPoints(coremetrics.Range{Start: start, End: start, ResolutionSeconds: 60}, nil); got == nil || len(got) != 0 {
		t.Fatalf("empty window = %#v, want an empty list", got)
	}
}

func TestNodeRollout(t *testing.T) {
	ready := uint64(2)
	for _, c := range []struct {
		name       string
		n          NodeRecord
		state      string
		diagnostic string
	}{
		{"offline", NodeRecord{TargetState: "ready", ReadyGeneration: &ready}, "unknown", ""},
		{"protocol 1 on another generation", NodeRecord{Online: true, ProtocolVersion: 1, DeploymentGeneration: 1, TargetGeneration: 2, TargetState: "ready", ReadyGeneration: &ready}, "update_required", ""},
		{"protocol 1 on the target", NodeRecord{Online: true, ProtocolVersion: 1, DeploymentGeneration: 2, TargetGeneration: 2, TargetState: "ready", ReadyGeneration: &ready}, "ready", ""},
		{"preparing", NodeRecord{Online: true, ProtocolVersion: 2, TargetState: "preparing", ReadyGeneration: &ready}, "preparing", ""},
		{"failed with an unknown diagnostic", NodeRecord{Online: true, ProtocolVersion: 2, TargetState: "failed", TargetDiagnostic: "boom", ReadyGeneration: &ready}, "failed", sandbox.NodeProviderUnavailable},
		{"failed without diagnostic", NodeRecord{Online: true, ProtocolVersion: 2, TargetState: "failed", ReadyGeneration: &ready}, "failed", ""},
		{"ready ignores a diagnostic", NodeRecord{Online: true, ProtocolVersion: 2, TargetState: "ready", TargetDiagnostic: "boom", ReadyGeneration: &ready}, "ready", ""},
		{"unknown target state", NodeRecord{Online: true, ProtocolVersion: 2, TargetState: "other", ReadyGeneration: &ready}, "unknown", ""},
		{"protocol 1 failed on the target", NodeRecord{Online: true, ProtocolVersion: 1, DeploymentGeneration: 2, TargetGeneration: 2, TargetState: "failed", TargetDiagnostic: "kvm_unavailable", ReadyGeneration: &ready}, "failed", "kvm_unavailable"},
		{"protocol 1 without a target state", NodeRecord{Online: true, ProtocolVersion: 1, DeploymentGeneration: 2, TargetGeneration: 2, ReadyGeneration: &ready}, "unknown", ""},
	} {
		got := nodeRollout(c.n)
		if got.State != c.state || got.Diagnostic != c.diagnostic || got.ReadyGeneration != &ready {
			t.Errorf("%s: nodeRollout = %+v", c.name, got)
		}
	}
}
