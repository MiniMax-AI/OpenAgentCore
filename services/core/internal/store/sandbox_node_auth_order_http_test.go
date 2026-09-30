package store_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/google/uuid"
)

// Before the sandbox deployment is initialized, node machine routes still
// authenticate first: a missing or invalid credential gets 401, and only a
// recognized credential learns that the deployment is unavailable.
func TestSandboxNodeRoutesAuthenticateBeforeDeploymentState(t *testing.T) {
	s, db := newManagedTestStoreDB(t)
	admin, err := api.NewDeploymentAuthenticator([]string{runtimedevice.HashCredential(uuid.NewString())})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := publicHandler(t, s, db, nil, "codex", storeKeys(s), storeExecution(t, s), managedSandboxes(t, s, db), withCoreKeys(admin))
	if err != nil {
		t.Fatal(err)
	}
	// Recognized, unconsumed enrollment tokens; no deployment has been initialized.
	enrollment := func(token, installation string) {
		t.Helper()
		if _, err := db.pool.Exec(t.Context(), "INSERT INTO runtime_node_enrollments(token_sha256,installation_id,expires_at) VALUES(encode(sha256($1::bytea),'hex'),$2,clock_timestamp()+interval '10 minutes')", token, installation); err != nil {
			t.Fatal(err)
		}
	}
	token, claimedToken, claimed := strings.Repeat("e", 64), strings.Repeat("c", 64), uuid.NewString()
	enrollment(token, uuid.NewString())
	enrollment(claimedToken, claimed)
	enroll, _ := json.Marshal(deployment.Enrollment{NodeID: uuid.NewString(), Credential: strings.Repeat("n", 64), Name: "Early node", Provider: "docker",
		BackendFingerprint: strings.Repeat("b", 64), DeploymentGeneration: 1, SpecificationDigest: strings.Repeat("d", 64), CoreURL: "https://core.example"})
	nodeID := uuid.NewString()
	type check struct {
		name, method, path, authorization, nodeHeader, body string
		want                                                int
	}
	run := func(checks []check) {
		t.Helper()
		for _, test := range checks {
			r := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.authorization != "" {
				r.Header.Set("Authorization", test.authorization)
			}
			if test.nodeHeader != "" {
				r.Header.Set("X-OAC-Node-ID", test.nodeHeader)
			}
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != test.want {
				t.Errorf("%s = %d, want %d: %s", test.name, w.Code, test.want, w.Body)
			}
		}
	}
	run([]check{
		{"configuration without credential", "GET", "/api/v1/sandbox-node/configuration", "", "", "", http.StatusUnauthorized},
		{"configuration with invalid token", "GET", "/api/v1/sandbox-node/configuration", "Bearer invalid", "", "", http.StatusUnauthorized},
		{"configuration with invalid node credential", "GET", "/api/v1/sandbox-node/configuration", "Bearer invalid", nodeID, "", http.StatusUnauthorized},
		{"configuration with recognized token", "GET", "/api/v1/sandbox-node/configuration", "Bearer " + token, "", "", http.StatusServiceUnavailable},
		{"enroll without credential", "POST", "/api/v1/sandbox-node/enroll", "", "", string(enroll), http.StatusUnauthorized},
		{"enroll with invalid token", "POST", "/api/v1/sandbox-node/enroll", "Bearer invalid", "", string(enroll), http.StatusUnauthorized},
		{"enroll with recognized token", "POST", "/api/v1/sandbox-node/enroll", "Bearer " + token, "", string(enroll), http.StatusServiceUnavailable},
		{"identity without credential", "GET", "/api/v1/sandbox-node/identity?node_id=" + nodeID, "", "", "", http.StatusUnauthorized},
		{"identity with invalid credential", "GET", "/api/v1/sandbox-node/identity?node_id=" + nodeID, "Bearer invalid", "", "", http.StatusUnauthorized},
	})

	// Once Web claims an installation, still before initialization, another
	// installation's token gets the same 401 it gets after initialization.
	if _, err := db.pool.Exec(t.Context(), "UPDATE runtime_deployment SET installation_id=$1, web_managed=true WHERE singleton=true", claimed); err != nil {
		t.Fatal(err)
	}
	run([]check{
		{"configuration with foreign token", "GET", "/api/v1/sandbox-node/configuration", "Bearer " + token, "", "", http.StatusUnauthorized},
		{"enroll with foreign token", "POST", "/api/v1/sandbox-node/enroll", "Bearer " + token, "", string(enroll), http.StatusUnauthorized},
		{"configuration with claimed token", "GET", "/api/v1/sandbox-node/configuration", "Bearer " + claimedToken, "", "", http.StatusServiceUnavailable},
		{"enroll with claimed token", "POST", "/api/v1/sandbox-node/enroll", "Bearer " + claimedToken, "", string(enroll), http.StatusServiceUnavailable},
	})
}
