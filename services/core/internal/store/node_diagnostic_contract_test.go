package store

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
)

func TestNodeDiagnosticSchemaContract(t *testing.T) {
	raw, err := os.ReadFile("../sandbox/testdata/node-diagnostics.json")
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
		check(model.Name(), strings.Split(field.Tag.Get("enums"), ","))
	}
	raw, err = os.ReadFile("../../../../contracts/agents-api/core.openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Definitions map[string]struct {
			Properties map[string]struct {
				Enum []string `yaml:"enum"`
			} `yaml:"properties"`
		} `yaml:"definitions"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"deployment.Node", "deployment.NodeDetail", "deployment.NodeRollout"} {
		check(name, document.Definitions[name].Properties["diagnostic"].Enum)
	}
}
