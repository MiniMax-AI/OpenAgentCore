package api

import (
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/nativeinstaller"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

// Registered routes that are deliberately not contract operations.
var unpublishedRoutes = map[string]string{
	"/healthz":                       "liveness probe, not part of the Agent API",
	"/api/v1/agent-daemon/install/*": "public immutable native release content, not an API operation",
}

// contractOperations reads one committed contract as "METHOD /path" keys and
// requires every published path to stay inside its credential namespace.
func contractOperations(t *testing.T, file, prefix string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile("../../../../contracts/agents-api/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		BasePath string                    `yaml:"basePath"`
		Paths    map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(file, err)
	}
	operations := map[string]bool{}
	for path, item := range document.Paths {
		full := strings.TrimSuffix(document.BasePath, "/") + path
		if !strings.HasPrefix(full, prefix+"/") {
			t.Errorf("%s publishes %s outside %s", file, full, prefix)
		}
		for method := range item {
			if slices.Contains([]string{"get", "post", "put", "patch", "delete", "head", "options"}, method) {
				operations[strings.ToUpper(method)+" "+full] = true
			}
		}
	}
	if len(operations) == 0 {
		t.Fatalf("%s publishes no operations", file)
	}
	return operations
}

// The Core and machine contracts are generated from handler annotations, so
// they must publish exactly the routes the server registers. /v1 is checked
// against the pinned upstream set by TestEveryRouteAuthenticatesItsCanonicalPath
// and the contract tests.
func TestContractsPublishExactlyTheRegisteredCoreAndMachineRoutes(t *testing.T) {
	admin, err := NewDeploymentAuthenticator([]string{device.HashCredential(routingAdminKey)})
	if err != nil {
		t.Fatal(err)
	}
	// Every option that gates a route registration, as the server passes them.
	s := &store.Store{}
	h := &Handler{store: s, engine: "codex"}
	for _, option := range []Option{WithSandboxManager(s, admin), WithProjectAPIKeys(s, admin), WithWriteAudit(s, admin), WithAdminManagement(s),
		WithInstallation(Installation{}, s.AddressBindings), WithNativeInstaller(&nativeinstaller.Catalog{}, "contract-test")} {
		option(h)
	}
	contractOperations(t, "openapi.yaml", "/v1")
	contracts := map[string]string{"/core/v1": "core.openapi.yaml", "/api/v1": "runtime.openapi.yaml"}
	published := map[string]map[string]bool{}
	for prefix, file := range contracts {
		published[prefix] = contractOperations(t, file, prefix)
	}
	guard := reflect.ValueOf(methodNotAllowed).Pointer()
	registered := map[string]bool{}
	err = chi.Walk(h.routes(), func(method, route string, handler http.Handler, _ ...func(http.Handler) http.Handler) error {
		if _, ok := unpublishedRoutes[route]; ok || strings.HasPrefix(route, "/v1/") {
			return nil
		}
		// An explicit HEAD or OPTIONS 405 guard is not an operation.
		if f, ok := handler.(http.HandlerFunc); ok && (method == http.MethodHead || method == http.MethodOptions) && reflect.ValueOf(f).Pointer() == guard {
			return nil
		}
		for prefix, operations := range published {
			if strings.HasPrefix(route, prefix+"/") {
				registered[method+" "+route] = true
				if !operations[method+" "+route] {
					t.Errorf("registered %s %s is not published in %s", method, route, contracts[prefix])
				}
				return nil
			}
		}
		t.Errorf("registered %s %s is outside every contract namespace", method, route)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for prefix, operations := range published {
		for operation := range operations {
			if !registered[operation] {
				t.Errorf("%s publishes %s, which the server does not register", contracts[prefix], operation)
			}
		}
	}
}
