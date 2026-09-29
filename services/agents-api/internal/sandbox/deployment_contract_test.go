package sandbox

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestInstallerDeploymentProjectionIsCurrent(t *testing.T) {
	raw, err := os.ReadFile("../../../../deploy/install/node_spec.py")
	if err != nil {
		t.Fatal(err)
	}
	expected := PythonDeploymentContract()
	if !strings.Contains(string(raw), expected) {
		t.Fatal("node_spec.py contract is stale; regenerate with go run ./services/agents-api/cmd/specification-contract -write")
	}
}
func TestDeploymentContractFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/deployment-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name, Provider, Canonical, Digest string
		Specification                     json.RawMessage
		Valid                             bool
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			var spec DeploymentSpec
			decoder := json.NewDecoder(bytes.NewReader(fixture.Specification))
			decoder.DisallowUnknownFields()
			err := decoder.Decode(&spec)
			if err == nil {
				err = spec.Validate(fixture.Provider)
			}
			if (err == nil) != fixture.Valid {
				t.Fatalf("validation differs: %v", err)
			}
			if !fixture.Valid {
				return
			}
			canonical, _ := json.Marshal(struct {
				Provider string `json:"provider"`
				DeploymentSpec
			}{fixture.Provider, spec})
			if string(canonical) != fixture.Canonical || spec.Digest(fixture.Provider) != fixture.Digest {
				t.Fatalf("canonical contract differs: %s", canonical)
			}
		})
	}
}
