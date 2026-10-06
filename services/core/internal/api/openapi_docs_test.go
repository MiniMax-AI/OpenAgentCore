package api

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

// /docs renders every published contract without a credential, and serves
// nothing but those documents.
func TestOpenAPIDocsServeThePublishedContracts(t *testing.T) {
	handler, _, _ := routingFixture(t)
	page := serve(handler, http.MethodGet, "/docs", "", nil)
	if page.Code != http.StatusOK || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("GET /docs = %d %v", page.Code, page.Header())
	}
	for _, readOnly := range []string{"supportedSubmitMethods: []", "validatorUrl: null", "authorizeBtn: () => null", "authorizeOperationBtn: () => null"} {
		if !strings.Contains(page.Body.String(), readOnly) {
			t.Errorf("the page lost read-only setting %q", readOnly)
		}
	}
	for _, file := range []string{"openapi.yaml", "core.openapi.yaml", "runtime.openapi.yaml"} {
		if !strings.Contains(page.Body.String(), `"/docs/`+file+`"`) {
			t.Errorf("the page does not render %s", file)
		}
		want, err := os.ReadFile("../../../../contracts/agents-api/" + file)
		if err != nil {
			t.Fatal(err)
		}
		got := serve(handler, http.MethodGet, "/docs/"+file, "", nil)
		if got.Code != http.StatusOK || got.Header().Get("Content-Type") != "application/yaml" || got.Body.String() != string(want) {
			t.Errorf("GET /docs/%s = %d %v", file, got.Code, got.Header())
		}
	}
	for _, path := range []string{"/docs/openapi.go", "/docs/upstream.json", "/docs/missing.yaml"} {
		if got := serve(handler, http.MethodGet, path, "", nil); got.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, got.Code)
		}
	}
}
