package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/google/uuid"
)

func callerBinding() APIKey {
	return APIKey{TokenSHA256: runtimedevice.HashCredential("caller"), TenantID: uuid.NewString(),
		OrganizationID: "org-one", ProjectID: "project-one", SubjectKind: "user", SubjectID: "user-one"}
}

func TestCallerPrincipalHeadersAndKeyRotation(t *testing.T) {
	key := callerBinding()
	rotated, peer := key, key
	rotated.TokenSHA256 = runtimedevice.HashCredential("rotated")
	peer.TokenSHA256 = runtimedevice.HashCredential("peer")
	deps, fakes := testDependencies(t)
	fakes.projectsReader.resolveAPIKey = projectKeys(t, key, rotated, peer).ResolveAPIKey
	for _, test := range []struct {
		name, token, subject string
		headers              http.Header
		status               int
	}{
		{"absent scopes", "caller", key.SubjectID, nil, 200},
		{"matching scopes", "caller", key.SubjectID, http.Header{"Openai-Organization": {key.OrganizationID}, "Openai-Project": {key.ProjectID}}, 200},
		{"rotated key", "rotated", key.SubjectID, nil, 200},
		{"same project peer", "peer", peer.SubjectID, nil, 200},
		{"forged identity", "caller", key.SubjectID, http.Header{"X-Tenant-Id": {uuid.NewString()}, "X-User-Id": {"other"}, "X-Forwarded-User": {"other"}}, 200},
		{"wrong organization", "caller", "", http.Header{"Openai-Organization": {"org-two"}}, 401},
		{"wrong project", "caller", "", http.Header{"Openai-Project": {"project-two"}}, 401},
		{"empty project", "caller", "", http.Header{"Openai-Project": {""}}, 401},
		{"duplicate project", "caller", "", http.Header{"Openai-Project": {key.ProjectID, key.ProjectID}}, 401},
		{"duplicate authorization", "caller", "", http.Header{"Authorization": {"Bearer caller", "Bearer peer"}}, 401},
		{"merged projects", "caller", "", http.Header{"Openai-Project": {key.ProjectID + ", other"}}, 401},
		{"wrong key", "unknown", "", nil, 401},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1/agents/sessions", nil)
			r.Header.Set("Authorization", "Bearer "+test.token)
			r.Header.Set("OpenAI-Beta", "agents=v1")
			for name, values := range test.headers {
				r.Header[name] = values
			}
			called := false
			h := (&Handler{Dependencies: deps}).authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				principal := r.Context().Value(principalContextKey{}).(identity.Principal)
				if principal.SubjectID != test.subject || tenantID(r) != key.TenantID || principal.ProjectID != key.ProjectID {
					t.Fatalf("authenticated principal = %+v", principal)
				}
				w.WriteHeader(http.StatusOK)
			}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != test.status || called != (test.status == 200) {
				t.Fatalf("response = %d, called = %v", w.Code, called)
			}
			// Beta 401s have type invalid_request_error and a null code (HP-07).
			if test.status == 401 && (w.Body.String() != `{"error":{"message":"A valid Agents API bearer key is required.","type":"invalid_request_error","code":null,"param":null}}`+"\n" || w.Header().Get("WWW-Authenticate") != "Bearer") {
				t.Fatalf("authentication response = %s", w.Body)
			}
		})
	}
}
