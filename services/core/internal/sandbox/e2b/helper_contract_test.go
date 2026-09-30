package e2b

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentnetwork"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestHelperProjectionFreshness(t *testing.T) {
	command := exec.Command("go", "run", "./internal/contractgen", "--check")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

func TestSharedHelperExchanges(t *testing.T) {
	data, err := os.ReadFile("../../../tools/e2b-provider/testdata/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Kind, Name string
		Valid      bool
		Payload    json.RawMessage
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Kind+"/"+fixture.Name, func(t *testing.T) {
			decoder := json.NewDecoder(bytes.NewReader(fixture.Payload))
			decoder.DisallowUnknownFields()
			valid := false
			switch fixture.Kind {
			case "request":
				var request Request
				valid = decoder.Decode(&request) == nil && request.Validate() == nil
			case "response":
				var response Response
				valid = decoder.Decode(&response) == nil && response.Validate() == nil
			case "managed":
				valid = validManagedExchange(fixture.Payload)
			default:
				t.Fatal("unknown fixture kind")
			}
			if valid != fixture.Valid {
				t.Fatalf("valid = %v, want %v", valid, fixture.Valid)
			}
		})
	}
}

// Reconstruct the Go bootstrap input and check the Python-managed projection
// against its owning types. Credentials travel only in RuntimeBootstrap.
func validManagedExchange(data []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return false
	}
	bootstrapBytes, _ := json.Marshal(sandbox.Bootstrap{})
	var expected map[string]json.RawMessage
	_ = json.Unmarshal(bootstrapBytes, &expected)
	delete(expected, "CoreURL")
	delete(expected, "Credential")
	expected["InstallationID"] = nil
	expected["RuntimeBootstrap"] = nil
	if len(fields) != len(expected) {
		return false
	}
	for name := range expected {
		if _, ok := fields[name]; !ok {
			return false
		}
	}
	var installation string
	if json.Unmarshal(fields["InstallationID"], &installation) != nil || !validID(installation) {
		return false
	}
	delete(fields, "InstallationID")
	delete(fields, "RuntimeBootstrap")
	data, _ = json.Marshal(fields)
	var bootstrap sandbox.Bootstrap
	if json.Unmarshal(data, &bootstrap) != nil || !validReference(bootstrap.Reference) || !validID(bootstrap.DeviceID) || !validID(bootstrap.SessionID) {
		return false
	}
	return (agentnetwork.Policy{Access: bootstrap.NetworkAccess, AllowedDomains: bootstrap.AllowedDomains}).Validate() == nil
}
