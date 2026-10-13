package node

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func TestGenerationGrantJSONRefusesAmbiguousDeletionAuthority(t *testing.T) {
	f := frame{Version: ProtocolVersion, Type: "retention_ack", Deployment: &sandbox.NodeDeployment{Generation: 2, SpecificationDigest: strings.Repeat("a", 64)}, Control: &generationControl{ID: uuid.NewString(), ConnectionID: uuid.NewString(), Sequence: 1, OwnerEpoch: 1, Retentions: []sandbox.GenerationRetention{{GenerationReference: sandbox.GenerationReference{Generation: 1, SpecificationDigest: strings.Repeat("b", 64)}, Keep: true}}}}
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	original := string(raw)
	if _, err := decodeFrame(raw); err != nil {
		t.Fatal("valid grant rejected", err)
	}
	for name, replacement := range map[string]string{
		"duplicate":             strings.Replace(original, `"keep":true`, `"keep":true,"keep":false`, 1),
		"case alias":            strings.Replace(original, `"keep":true`, `"Keep":false`, 1),
		"null grant":            strings.Replace(original, `"keep":true`, `"keep":null`, 1),
		"missing grant":         strings.Replace(original, `,"keep":true`, ``, 1),
		"foreign null":          strings.TrimSuffix(original, "}") + `,"request":null}`,
		"mixed null control":    strings.Replace(original, `"retentions":`, `"references":null,"retentions":`, 1),
		"null serving omission": strings.Replace(original, `,"serving_generation":null`, ``, 1),
		"duplicate envelope":    strings.TrimSuffix(original, "}") + `,"version":2}`,
		"sequence overflow":     strings.Replace(original, `"sequence":1`, `"sequence":9223372036854775808`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if replacement == original {
				t.Fatal("fixture did not change")
			}
			if _, err := decodeFrame([]byte(replacement)); err == nil {
				t.Fatal("ambiguous grant accepted")
			}
		})
	}
}

func TestGenerationHealthRejectsUnboundedOrAmbiguousNumbers(t *testing.T) {
	f := frame{Version: ProtocolVersion, Type: "heartbeat", ConnectionID: uuid.NewString(), OwnerEpoch: 1, Health: &Health{ObservedAt: time.Now().UTC()}}
	good, _ := json.Marshal(f)
	if _, err := decodeFrame(good); err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []struct{ from, to string }{
		{`"active_operations":0`, `"active_operations":-1`},
		{`"active_operations":0`, `"active_operations":33`},
		{`"cpu_utilization":null`, `"cpu_utilization":1.1`},
		{`"total_memory_bytes":null`, `"total_memory_bytes":9007199254740992`},
		{`"effective_cpu_cores":null`, `"effective_cpu_cores":0`},
		{`"provider_ready":false`, `"Provider_Ready":false`},
	} {
		if _, err := decodeFrame([]byte(strings.Replace(string(good), replacement.from, replacement.to, 1))); err == nil {
			t.Fatal("invalid health accepted", replacement.to)
		}
	}
}

func TestNodeProtocolRejectsHistoricalVersions(t *testing.T) {
	for _, version := range []int{1, 2, 3, 4, 5} {
		raw, _ := json.Marshal(frame{Version: version, Type: "hello", Identity: new(Identity), Health: &Health{ObservedAt: time.Now().UTC()}})
		if _, err := decodeFrame(raw); err == nil {
			t.Fatalf("accepted historical protocol %d", version)
		}
	}
}

func TestNodeGenerationManagementIsAnExplicitCurrentCapability(t *testing.T) {
	for _, managed := range []bool{false, true} {
		f := frame{Version: ProtocolVersion, Type: "hello", GenerationManagement: managed, Identity: new(Identity), Health: &Health{ObservedAt: time.Now().UTC()}}
		if managed {
			f.Health.Generations = []sandbox.GenerationStatus{{Generation: 1, SpecificationDigest: strings.Repeat("a", 64), State: "ready"}}
		}
		raw, _ := json.Marshal(f)
		if _, err := decodeFrame(raw); err != nil {
			t.Fatalf("current mode managed=%v: %v", managed, err)
		}
		if managed {
			f.GenerationManagement = false
			raw, _ = json.Marshal(f)
			decoded, err := decodeFrame(raw)
			if err != nil || decoded.GenerationManagement || len(decoded.Health.Generations) != 1 {
				t.Fatal("static qualification implicitly enabled generation management", err)
			}
		}
	}
}

func TestCheckpointGenerationHealthJSONRoundTrip(t *testing.T) {
	for _, managed := range []bool{false, true} {
		for _, kind := range []string{"hello", "heartbeat"} {
			t.Run(fmt.Sprintf("managed=%t/%s", managed, kind), func(t *testing.T) {
				compatibility := sandbox.CheckpointCompatibility{ArtifactDomain: "private-store", ExecutionClass: "native-class"}
				f := frame{Version: ProtocolVersion, Type: kind, Health: &Health{ProviderReady: true, ObservedAt: time.Now().UTC(), Generations: []sandbox.GenerationStatus{{Generation: 5, SpecificationDigest: strings.Repeat("a", 64), State: "ready", Checkpoint: &compatibility}}}}
				if kind == "hello" {
					f.Identity = new(Identity)
					f.GenerationManagement = managed
				} else {
					f.ConnectionID = uuid.NewString()
					f.OwnerEpoch = 1
				}
				raw, err := json.Marshal(f)
				if err != nil {
					t.Fatal(err)
				}
				if err := validateVersionFrame(f, len(raw)); err != nil {
					t.Fatal("writer rejected valid status", err)
				}
				decoded, err := decodeFrame(raw)
				if err != nil {
					t.Fatal("decoder rejected valid status", err)
				}
				if decoded.Health == nil || len(decoded.Health.Generations) != 1 || decoded.Health.Generations[0].Checkpoint == nil || *decoded.Health.Generations[0].Checkpoint != compatibility {
					t.Fatal("checkpoint qualification lost", decoded)
				}
				original := string(raw)
				for name, replacement := range map[string]string{
					"unknown nested field":     strings.Replace(original, `"artifact_domain":"private-store"`, `"artifact_domain":"private-store","extra":"value"`, 1),
					"unknown generation field": strings.Replace(original, `"checkpoint":`, `"extra":true,"checkpoint":`, 1),
					"duplicate token":          strings.Replace(original, `"artifact_domain":"private-store"`, `"artifact_domain":"private-store","artifact_domain":"other"`, 1),
					"case alias":               strings.Replace(original, `"artifact_domain":`, `"Artifact_Domain":`, 1),
					"missing token":            strings.Replace(original, `"artifact_domain":"private-store",`, "", 1),
					"null token":               strings.Replace(original, `"artifact_domain":"private-store"`, `"artifact_domain":null`, 1),
					"null checkpoint":          strings.Replace(original, `{"artifact_domain":"private-store","execution_class":"native-class"}`, `null`, 1),
					"empty domain":             strings.Replace(original, `"private-store"`, `""`, 1),
					"empty execution":          strings.Replace(original, `"native-class"`, `""`, 1),
					"oversized domain":         strings.Replace(original, `"private-store"`, `"`+strings.Repeat("x", 257)+`"`, 1),
					"oversized execution":      strings.Replace(original, `"native-class"`, `"`+strings.Repeat("x", 257)+`"`, 1),
					"nonready checkpoint":      strings.Replace(original, `"state":"ready"`, `"state":"preparing"`, 1),
				} {
					t.Run(name, func(t *testing.T) {
						if replacement == original {
							t.Fatal("fixture unchanged")
						}
						if _, err := decodeFrame([]byte(replacement)); err == nil {
							t.Fatal("invalid checkpoint accepted")
						}
					})
				}
			})
		}
	}
}
