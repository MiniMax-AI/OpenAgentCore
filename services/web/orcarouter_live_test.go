package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestOrcaCatalogLiveR... reads the real OrcaRouter catalog through the console's
// own provider path. It runs only when ORCAROUTER_API_KEY is present, so the
// regular suite stays offline and no key is ever required to build or test.
func TestOrcaCatalogLiveReadsTheRealGateway(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("ORCAROUTER_API_KEY"))
	if key == "" {
		t.Skip("ORCAROUTER_API_KEY is not set")
	}
	if !strings.HasPrefix(key, "sk-orca-") {
		t.Fatalf("ORCAROUTER_API_KEY is not an OrcaRouter key")
	}
	router, err := loadOrcaRouter()
	if err != nil {
		t.Fatal(err)
	}
	if router.apiOrigin != defaultOrcaAPIOrigin || router.authOrigin != defaultOrcaAuthOrigin {
		t.Fatalf("live read must use the public origins, got %q %q", router.authOrigin, router.apiOrigin)
	}
	h := &console{orca: router}
	recorder := serveOrca(h, "GET", "/console/orcarouter/catalog?capability=chat", nil,
		map[string]string{"X-OrcaRouter-Key": key})
	if recorder.Code != 200 {
		t.Fatalf("live catalog status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var response orcaCatalogResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Degraded || len(response.Models) == 0 {
		t.Fatalf("live catalog = %#v", response)
	}
	if response.Origin != defaultOrcaAPIOrigin {
		t.Fatalf("live catalog origin = %q", response.Origin)
	}
	if strings.Contains(recorder.Body.String(), key) {
		t.Fatal("the key was echoed back")
	}
	// Every record that reaches the browser carries a text protocol and enough
	// metadata for the chat entrance to filter on.
	text, imageOnly := 0, 0
	for _, model := range response.Models {
		if len(model.EndpointTypes) == 0 {
			t.Fatalf("%s reached the browser with no endpoint types", model.ID)
		}
		if chatModel(model.EndpointTypes) {
			text++
			continue
		}
		for _, endpoint := range model.EndpointTypes {
			if endpoint == "image-generation" || endpoint == "openai-video" || endpoint == "jina-rerank" {
				imageOnly++
			}
		}
	}
	if text == 0 {
		t.Fatal("the live catalog carried no text model")
	}
	t.Logf("live OrcaRouter catalog: %d records, %d text", len(response.Models), text)
	if imageOnly != 0 {
		t.Errorf("%d non-text models were offered to a chat entrance", imageOnly)
	}
}

// chatModel reports whether the model speaks a text protocol. The console's
// selector applies the same rule in the browser.
func chatModel(endpoints []string) bool {
	for _, endpoint := range endpoints {
		switch endpoint {
		case "openai", "anthropic", "gemini", "openai-response":
			return true
		}
	}
	return false
}
