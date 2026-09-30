package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNodeConfigurationRejectsRetiredHeaderWithoutExposingValues(t *testing.T) {
	for _, value := range []string{"", "private-node-value"} {
		t.Run(value, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/sandbox-node/configuration", nil)
			request.Header.Set("X-Parsar-Node-ID", value)
			request.Header.Set("X-OAC-Node-ID", "new-node")
			response := httptest.NewRecorder()
			(&Handler{}).sandboxNodeConfiguration(response, request)
			body := response.Body.String()
			if response.Code != http.StatusBadRequest || !strings.Contains(body, `"code":"invalid_request"`) ||
				!strings.Contains(body, "X-Parsar-Node-ID was renamed to X-OAC-Node-ID; use the node command from this Core's Web") ||
				strings.Contains(body, "private-node-value") || strings.Contains(body, "new-node") {
				t.Fatalf("unexpected retired header response: %d %s", response.Code, body)
			}
		})
	}
}
