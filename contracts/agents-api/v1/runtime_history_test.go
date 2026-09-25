package v1

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRuntimeHistoryOpenAPICollectionLimits(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../core.openapi.yaml")
	if err != nil {
		t.Fatalf("read generated OpenAPI contract: %v", err)
	}

	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse generated OpenAPI contract: %v", err)
	}

	definitions := openAPIMap(t, document, "definitions")
	assertOpenAPIArrayLimit(t, definitions, "v1.RuntimeHistory", "series", 1000)
	assertOpenAPIArrayLimit(t, definitions, "v1.RuntimeHistoryCoverage", "buckets", 10000)
	assertOpenAPIArrayLimit(t, definitions, "v1.RuntimeHistorySeries", "points", 10000)
	series := openAPIMap(t, definitions, "v1.RuntimeHistorySeries")
	providerType := openAPIMap(t, openAPIMap(t, series, "properties"), "provider_type")
	if actual, ok := providerType["pattern"].(string); !ok || actual != "^[a-z][a-z0-9_]{0,31}$" {
		t.Fatalf("Runtime history provider_type pattern = %#v, want canonical provider pattern", providerType["pattern"])
	}

	if !strings.Contains(string(raw), "100,000 total coverage plus series points") {
		t.Fatal("runtime history operation must document the 100,000 total point limit")
	}
}

func assertOpenAPIArrayLimit(t *testing.T, definitions map[string]any, definition, property string, expected int) {
	t.Helper()

	schema := openAPIMap(t, definitions, definition)
	properties := openAPIMap(t, schema, "properties")
	field := openAPIMap(t, properties, property)
	actual, ok := field["maxItems"].(int)
	if !ok {
		t.Fatalf("%s.%s maxItems has type %T, want int", definition, property, field["maxItems"])
	}
	if actual != expected {
		t.Fatalf("%s.%s maxItems = %d, want %d", definition, property, actual, expected)
	}
}

func openAPIMap(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()

	value, ok := parent[key]
	if !ok {
		t.Fatalf("OpenAPI key %q is missing", key)
	}
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI key %q has type %T, want object", key, value)
	}
	return result
}
