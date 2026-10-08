package providers

import (
	"bytes"
	"encoding/json"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"os"
	"strings"
	"testing"
)

func TestDeploymentContractProjectionsAreCurrent(t *testing.T) {
	registry := Builtin()
	python, err := os.ReadFile("../../../../../deploy/node/node_spec.py")
	if err != nil {
		t.Fatal(err)
	}
	typescript, err := os.ReadFile("../../../../../packages/agents-client/src/deployment-contract.ts")
	if err != nil {
		t.Fatal(err)
	}
	expectedPython, expectedTypeScript, err := registry.DeploymentContract()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(python), expectedPython) || string(typescript) != expectedTypeScript {
		t.Fatal("deployment contract projection is stale; regenerate with go run ./services/core/cmd/specification-contract -write")
	}
}
func TestDeploymentContractFixtures(t *testing.T) {
	registry := Builtin()
	raw, err := os.ReadFile("../testdata/deployment-contract.json")
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
			var spec sandbox.DeploymentSpec
			decoder := json.NewDecoder(bytes.NewReader(fixture.Specification))
			decoder.DisallowUnknownFields()
			err := decoder.Decode(&spec)
			if err == nil {
				err = registry.ValidateSpecification(fixture.Provider, spec)
			}
			if (err == nil) != fixture.Valid {
				t.Fatalf("validation differs: %v", err)
			}
			if !fixture.Valid {
				return
			}
			canonical, _ := json.Marshal(struct {
				Provider string `json:"provider"`
				sandbox.DeploymentSpec
			}{fixture.Provider, spec})
			if string(canonical) != fixture.Canonical || spec.Digest(fixture.Provider) != fixture.Digest {
				t.Fatalf("canonical contract differs: %s", canonical)
			}
		})
	}
}
