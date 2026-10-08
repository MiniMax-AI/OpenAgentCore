package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func contractDocument(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("../" + path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func TestOfficialSchemaPin(t *testing.T) {
	pin := contractDocument(t, "upstream.json")["openapi"].(map[string]any)
	raw, err := os.ReadFile("../upstream/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != pin["sha256"] {
		t.Fatal("official schema differs from its pin")
	}
}

func TestPublicSchemaPreservesOfficialDefinitions(t *testing.T) {
	source := contractDocument(t, "upstream/openapi.json")
	public := contractDocument(t, "openapi.yaml")
	if public["openapi"] != "3.1.0" {
		t.Fatal("public contract must retain OpenAPI 3.1")
	}
	upstream := source["components"].(map[string]any)["schemas"].(map[string]any)
	schemas := public["components"].(map[string]any)["schemas"].(map[string]any)
	owners := map[string]bool{"AgentResource": true, "CreateAgentParams": true, "UpdateAgentParams": true, "SessionAgentConfigParam": true, "SessionAgentResource": true, "CreateAgentSessionParams": true, "SessionResource": true}
	// Extension dependencies retain their producing Go package names.
	for name, value := range schemas {
		if strings.HasPrefix(name, "v1.") || name == "modelprovider.Protocol" {
			continue
		}
		schema := value.(map[string]any)
		properties, _ := schema["properties"].(map[string]any)
		if _, exists := properties["x_agents_core"]; exists {
			if !owners[name] {
				t.Errorf("extension on unexpected resource %s", name)
			}
			delete(properties, "x_agents_core")
			delete(owners, name)
		}
		if !reflect.DeepEqual(schema, upstream[name]) {
			t.Errorf("official definition %s was changed", name)
		}
	}
	if len(owners) != 0 {
		t.Errorf("missing extension owners: %v", owners)
	}
}

func TestPublicOperationsPreserveOfficialContract(t *testing.T) {
	source := contractDocument(t, "upstream/openapi.json")["paths"].(map[string]any)
	published := contractDocument(t, "openapi.yaml")["paths"].(map[string]any)
	count := 0
	for path, item := range source {
		if !(strings.HasPrefix(path, "/agents") || strings.HasPrefix(path, "/files") || strings.HasPrefix(path, "/skills") || strings.HasPrefix(path, "/vaults")) {
			continue
		}
		actual, ok := published[path].(map[string]any)
		if !ok {
			t.Errorf("missing official path %s", path)
			continue
		}
		for method, raw := range item.(map[string]any) {
			expected := raw.(map[string]any)
			operation := actual[method].(map[string]any)
			if strings.HasPrefix(path, "/agents") || strings.HasPrefix(path, "/vaults") {
				params := operation["parameters"].([]any)
				beta := params[len(params)-1].(map[string]any)
				if beta["name"] != "OpenAI-Beta" || beta["required"] != true {
					t.Errorf("missing Beta header on %s %s", method, path)
				}
				if _, present := expected["parameters"]; len(params) == 1 && !present {
					delete(operation, "parameters")
				} else {
					operation["parameters"] = params[:len(params)-1]
				}
			}

			delete(expected, "security")
			if !reflect.DeepEqual(operation, expected) {
				t.Errorf("official operation changed: %s %s", method, path)
			}
			count++
		}
		delete(published, path)
	}
	if count != 58 || len(published) != 0 {
		t.Fatalf("expected pinned 58 operations, got %d, extra paths %v", count, published)
	}
}
