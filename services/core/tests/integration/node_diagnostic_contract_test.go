package integration

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"gopkg.in/yaml.v3"
)

func TestNodeDiagnosticSchemaContract(t *testing.T) {
	raw, err := os.ReadFile("../../internal/sandbox/testdata/node-diagnostics.json")
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	if err := json.Unmarshal(raw, &codes); err != nil {
		t.Fatal(err)
	}
	slices.Sort(codes)
	check := func(name string, values []string) {
		t.Helper()
		slices.Sort(values)
		if !slices.Equal(values, codes) {
			t.Errorf("%s diagnostic enum = %v, want shared fixture %v", name, values, codes)
		}
	}
	for _, model := range []reflect.Type{reflect.TypeFor[deployment.NodeHealth](), reflect.TypeFor[deployment.NodeRollout]()} {
		field, ok := model.FieldByName("Diagnostic")
		if !ok {
			t.Fatalf("%s has no Diagnostic field", model.Name())
		}
		if field.Type != reflect.TypeFor[sandbox.NodeDiagnosticCode]() {
			t.Errorf("%s diagnostic must use the canonical code type", model.Name())
		}
	}
	raw, err = os.ReadFile("../../../../contracts/agents-api/core.openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Definitions map[string]struct {
			Enum       []string `yaml:"enum"`
			Properties map[string]struct {
				Ref   string `yaml:"$ref"`
				AllOf []struct {
					Ref string `yaml:"$ref"`
				} `yaml:"allOf"`
			} `yaml:"properties"`
		} `yaml:"definitions"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	check("sandbox.NodeDiagnosticCode", document.Definitions["sandbox.NodeDiagnosticCode"].Enum)
	for _, name := range []string{"deployment.Node", "deployment.NodeDetail", "deployment.NodeRollout"} {
		field := document.Definitions[name].Properties["diagnostic"]
		ref := field.Ref
		if len(field.AllOf) == 1 {
			ref = field.AllOf[0].Ref
		}
		if ref != "#/definitions/sandbox.NodeDiagnosticCode" {
			t.Errorf("%s diagnostic does not reference the canonical enum", name)
		}
	}
}
