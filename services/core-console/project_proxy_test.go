package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicAPINeverPassesThroughConsole(t *testing.T) {
	c := accountConsoleConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("public API reached Core through console") }))
	h := accountConsole(t, c)
	cookie := setupAccount(t, h)
	for _, path := range []string{"/v1", "/v1/agents", "/v1/agents/sessions", "/v1/files/file/content", "/core/v1/environments/env/executor-credentials", "/console/api-keys"} {
		for _, method := range []string{"GET", "POST", "DELETE"} {
			for _, authorization := range []string{"", "Bearer project-key", "Bearer deployment-token", "Basic YWRtaW46cGFzc3dvcmQ="} {
				r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
				r.Host = h.host
				r.Header.Set("Origin", h.origin)
				r.Header.Set("Authorization", authorization)
				r.AddCookie(cookie)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != 404 {
					t.Errorf("%s %s = %d", method, path, w.Code)
				}
			}
		}
	}
}

func TestManagedSessionArchiveAllowlist(t *testing.T) {
	path := "/core/v1/admin/projects/project/sessions/session/archive"
	for _, test := range []struct {
		method, path string
		allowed      bool
	}{
		{"GET", path, true}, {"HEAD", path, true}, {"POST", path, true},
		{"DELETE", path, false}, {"PUT", path, false}, {"PATCH", path, false},
		{"POST", path + "/extra", false},
		{"POST", "/core/v1/admin/projects/project/sessions/session/events", false},
		{"POST", "/v1/agents/sessions/session/archive", false},
	} {
		if got := adminAPIRequest(httptest.NewRequest(test.method, test.path, nil)); got != test.allowed {
			t.Errorf("%s %s: allowed=%v", test.method, test.path, got)
		}
	}
}
