package node

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestEnrollmentRecoveryAcceptsCoreReadiness(t *testing.T) {
	for _, fields := range []string{"", `,"connected":true,"provider_ready":false`} {
		response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"node_id":"node","installation_id":"installation","provider":"docker"` + fields + `}`))}
		value, err := readEnrollment(response)
		if err != nil || value.NodeID != "node" || value.InstallationID != "installation" || value.Provider != "docker" {
			t.Fatal(value, err)
		}
	}
}
