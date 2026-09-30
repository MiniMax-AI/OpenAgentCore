package v1

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// upstream-routes.json and upstream-fields.json are produced from the pinned
// SDK by scripts/extract-agents-api-upstream.py.

var openAPIPathParameter = regexp.MustCompile(`\{[^}]*\}`)

func readContractJSON(t *testing.T, name string, value any) {
	t.Helper()
	raw, err := os.ReadFile("../" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, value); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
}

// publicOperations indexes the public OpenAPI operations by "METHOD /path",
// with path parameters normalised to {} like the pinned route set.
func publicOperations(t *testing.T) (map[string]map[string]any, map[string]any) {
	t.Helper()
	raw, err := os.ReadFile("../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	operations := map[string]map[string]any{}
	for path, item := range openAPIMap(t, document, "paths") {
		for method, operation := range item.(map[string]any) {
			if method == "parameters" {
				continue
			}
			operations[strings.ToUpper(method)+" "+openAPIPathParameter.ReplaceAllString(path, "{}")] = operation.(map[string]any)
		}
	}
	return operations, openAPIMap(t, document, "definitions")
}

func TestPublicOpenAPIRoutesMatchPinnedUpstream(t *testing.T) {
	var pinned struct {
		Routes []string `json:"routes"`
	}
	readContractJSON(t, "upstream-routes.json", &pinned)
	operations, _ := publicOperations(t)
	var missing, extra []string
	for _, route := range pinned.Routes {
		if operations[route] == nil {
			missing = append(missing, route)
		}
	}
	for route := range operations {
		if !slices.Contains(pinned.Routes, route) {
			extra = append(extra, route)
		}
	}
	sort.Strings(extra)
	if len(pinned.Routes) == 0 || len(missing) != 0 || len(extra) != 0 {
		t.Fatalf("public OpenAPI differs from the pinned SDK route set: missing %v, extra %v", missing, extra)
	}
}

// fieldPlacementPending temporarily lists properties that are neither in the
// pinned SDK type nor inside x_agents_core, keyed by schema.property. Each one
// awaits a decision: remove it, move it into x_agents_core or rename it to the
// official field. Remove an entry once it is resolved; stale entries fail.
var fieldPlacementPending = map[string]string{
	// Naming mismatch, not an extension: v1.ItemContent is shared by Item
	// content, where these fields are official, and the content_part and
	// reasoning_summary_part event parts, whose official types have only text
	// and type.
	"v1.ItemContent.encrypted_content": "shared with Item content; not on official event parts",
	"v1.ItemContent.image_url":         "shared with Item content; not on official event parts",
	// Naming mismatch, not an extension: v1.StreamError is shared by error
	// events (official SessionError.param) and Environment state errors, whose
	// official type has no param; Core omits it there.
	"v1.StreamError.param": "shared with error events; not on official Environment state errors",
}

// Official list pages also carry object, first_id and last_id (recorded in
// official-semantics-alignment.md), which the SDK's hand-written page classes
// do not declare.
var listEnvelopeFields = []string{"object", "first_id", "last_id"}

// Core fields live only inside x_agents_core on these Agent and Session
// definitions, and only as these members.
var (
	coreExtensionOwners  = []string{"v1.Agent", "v1.InlineAgent", "v1.SavedAgent", "v1.CreateAgentRequest", "v1.UpdateAgentRequest", "v1.Session", "v1.CreateSessionRequest"}
	coreExtensionMembers = []string{"harness", "model_provider", "harness_config", "environment"}
)

type upstreamFields struct {
	Operations map[string]struct{ Request, Query, Response []string } `json:"operations"`
	Types      map[string]map[string][]string                         `json:"types"`
}

type fieldAudit struct {
	definitions map[string]any
	types       map[string]map[string][]string
	visited     map[string]bool
	violations  map[string][]string
}

// auditPublicFields returns every public property, query parameter or
// extension member that the pinned SDK types do not have, keyed by schema.field.
func auditPublicFields(operations map[string]map[string]any, definitions map[string]any, pinned upstreamFields) map[string][]string {
	audit := fieldAudit{definitions: definitions, types: pinned.Types, visited: map[string]bool{}, violations: map[string][]string{}}
	for route, operation := range operations {
		official, ok := pinned.Operations[route]
		if !ok {
			continue // The route set test reports it.
		}
		form, query := map[string]any{}, map[string]any{}
		for _, parameter := range asSlice(operation["parameters"]) {
			parameter := parameter.(map[string]any)
			switch parameter["in"] {
			case "body":
				audit.compare(route+" body", parameter["schema"].(map[string]any), official.Request)
			case "formData":
				form[parameter["name"].(string)] = map[string]any{}
			case "query":
				// The SDK sends list parameters with brackets, as status[].
				query[strings.TrimSuffix(parameter["name"].(string), "[]")] = map[string]any{}
			}
		}
		if len(form) != 0 {
			audit.compare(route+" form", map[string]any{"properties": form}, official.Request)
		}
		if len(query) != 0 {
			audit.compare(route+" query", map[string]any{"properties": query}, official.Query)
		}
		for code, response := range asMap(operation["responses"]) {
			if schema, ok := asMap(response)["schema"].(map[string]any); ok && strings.HasPrefix(code, "2") {
				audit.compare(route+" response", schema, official.Response)
			}
		}
	}
	return audit.violations
}

func TestPublicOpenAPIFieldsAreOfficialOrCoreExtensions(t *testing.T) {
	var pinned upstreamFields
	readContractJSON(t, "upstream-fields.json", &pinned)
	var routes struct {
		Routes []string `json:"routes"`
	}
	readContractJSON(t, "upstream-routes.json", &routes)
	var extracted []string
	for route := range pinned.Operations {
		extracted = append(extracted, route)
	}
	sort.Strings(extracted)
	if !slices.Equal(extracted, routes.Routes) {
		t.Fatalf("upstream-fields.json operations %v differ from the pinned routes %v", extracted, routes.Routes)
	}
	operations, definitions := publicOperations(t)
	violations := auditPublicFields(operations, definitions, pinned)
	for field, sources := range violations {
		if _, ok := fieldPlacementPending[field]; !ok {
			t.Errorf("%s is not an official field of %v; put Core-only fields inside x_agents_core", field, sources)
		}
	}
	for field := range fieldPlacementPending {
		if violations[field] == nil {
			t.Errorf("resolved field %s is still listed in fieldPlacementPending", field)
		}
	}
}

// The audit rejects a Core field outside x_agents_core, a new x_agents_core
// member and x_agents_core on another resource.
func TestPublicFieldAuditRejectsMisplacedCoreFields(t *testing.T) {
	var pinned upstreamFields
	readContractJSON(t, "upstream-fields.json", &pinned)
	for _, test := range []struct {
		definition, property, want string
	}{
		{"v1.SessionExecutionInput", "sandbox_node_id", "v1.CreateSessionRequest.x_agents_core.sandbox_node_id"},
		{"v1.AgentsCore", "sandbox_node_id", "v1.Agent.x_agents_core.sandbox_node_id"},
		{"v1.Vault", "x_agents_core", "v1.Vault.x_agents_core"},
	} {
		operations, definitions := publicOperations(t)
		asMap(asMap(definitions[test.definition])["properties"])[test.property] = map[string]any{"type": "string"}
		if violations := auditPublicFields(operations, definitions, pinned); violations[test.want] == nil {
			t.Errorf("%s.%s was not reported as %s; got %v", test.definition, test.property, test.want, violations)
		}
	}
}

// compare checks one of our schemas against the union of the official types
// that describe the same value, following nested official models.
func (a *fieldAudit) compare(name string, schema map[string]any, official []string) {
	name, schema = a.resolve(name, schema)
	key := name + "|" + strings.Join(official, ",")
	if schema == nil || a.visited[key] {
		return
	}
	a.visited[key] = true
	for property, value := range a.properties(schema) {
		var children []string
		known := false
		for _, typeName := range official {
			if nested, ok := a.types[typeName][property]; ok {
				known = true
				for _, child := range nested {
					if !slices.Contains(children, child) {
						children = append(children, child)
					}
				}
			}
		}
		switch {
		case known && len(children) != 0:
			sort.Strings(children)
			a.compare(name+"."+property, value, children)
		case known:
		case property == "x_agents_core" && slices.Contains(coreExtensionOwners, name):
			_, extension := a.resolve(name+"."+property, value)
			for member := range a.properties(extension) {
				if name == "v1.Session" && member == "installation" {
					continue
				}
				if !slices.Contains(coreExtensionMembers, member) {
					a.violations[name+".x_agents_core."+member] = coreExtensionMembers
				}
			}
		case slices.Contains(listEnvelopeFields, property) && slices.ContainsFunc(official, func(typeName string) bool { return strings.HasPrefix(typeName, "pagination.") }):
		default:
			a.violations[name+"."+property] = official
		}
	}
}

// resolve follows references, single allOf wrappers and arrays to the object
// schema, naming it by its definition when it has one.
func (a *fieldAudit) resolve(name string, schema map[string]any) (string, map[string]any) {
	for schema != nil {
		switch {
		case schema["$ref"] != nil:
			name = strings.TrimPrefix(schema["$ref"].(string), "#/definitions/")
			schema = asMap(a.definitions[name])
		case len(asSlice(schema["allOf"])) == 1:
			schema = asMap(asSlice(schema["allOf"])[0])
		case schema["items"] != nil:
			schema = asMap(schema["items"])
		default:
			return name, schema
		}
	}
	return name, nil
}

func (a *fieldAudit) properties(schema map[string]any) map[string]map[string]any {
	result := map[string]map[string]any{}
	for property, value := range asMap(schema["properties"]) {
		result[property] = asMap(value)
	}
	for _, part := range asSlice(schema["allOf"]) {
		_, resolved := a.resolve("", asMap(part))
		for property, value := range a.properties(resolved) {
			result[property] = value
		}
	}
	return result
}

func asMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func asSlice(value any) []any {
	result, _ := value.([]any)
	return result
}
