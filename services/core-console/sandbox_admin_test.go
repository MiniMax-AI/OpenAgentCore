package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func adminRequest(t *testing.T, serverURL, method, path, token string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(method, serverURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "127.0.0.1:8080"
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func TestSandboxAdminProxyPreservesIndependentCredential(t *testing.T) {
	var calls atomic.Int32
	server, _ := testConsole(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer separate-admin-key" {
			t.Errorf("admin credential was replaced: %q", r.Header.Get("Authorization"))
		}
		for _, header := range []string{"Cookie", "Proxy-Authorization", "Origin", "Referer"} {
			if r.Header.Get(header) != "" {
				t.Errorf("browser %s reached Core", header)
			}
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	for _, tc := range []struct{ method, path string }{
		{"GET", "/core/v1/sandbox/deployment"},
		{"GET", "/core/v1/sandbox/nodes?limit=5"},
		{"PATCH", "/core/v1/sandbox/nodes/node-id"},
		{"DELETE", "/core/v1/sandbox/nodes/node-id"},
		{"GET", "/core/v1/sandbox/nodes/node-id/allocations"},
		{"POST", "/core/v1/sandbox/enrollment-tokens"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := adminRequest(t, server.URL, tc.method, tc.path, "separate-admin-key")
			r.Header.Set("Cookie", "console=cached")
			r.Header.Set("Proxy-Authorization", "Basic private")
			r.Header.Set("Referer", testOrigin+"/sandbox")
			response, _ := responseBody(t, server, r)
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", response.StatusCode)
			}
		})
	}
	if calls.Load() != 6 {
		t.Fatalf("proxied %d operations", calls.Load())
	}
}

func TestSandboxAdminAdmissionRequiresOneExplicitBearer(t *testing.T) {
	var calls atomic.Int32
	server, _ := testConsole(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	for _, authorization := range [][]string{
		nil, {""}, {"Bearer"}, {"Bearer "}, {"Bearer a b"},
		{"Basic YWRtaW46cHJpdmF0ZS1jb25zb2xlLXBhc3N3b3Jk"},
		{"Bearer one", "Bearer two"}, {"Bearer one", "Bearer one"},
	} {
		r := adminRequest(t, server.URL, "GET", "/core/v1/sandbox/nodes", "ignored")
		r.Header["Authorization"] = authorization
		response, body := responseBody(t, server, r)
		if response.StatusCode != 401 || response.Header.Get("WWW-Authenticate") != "" || strings.Contains(body, "private-") {
			t.Fatalf("invalid bearer response = %d %q", response.StatusCode, body)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid or Basic-only credential reached the admin API")
	}
}

func TestSandboxAdminAndProjectAuthorityRemainSeparate(t *testing.T) {
	var calls atomic.Int32
	server, _ := testConsole(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		want := "Bearer separate-admin-key"
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			want = "Bearer private-core-token"
		}
		if r.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"code":"invalid_admin_key"}}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	// Core, not the console password or the project credential, decides admin access.
	r := adminRequest(t, server.URL, "GET", "/core/v1/sandbox/nodes", "private-core-token")
	response, body := responseBody(t, server, r)
	if response.StatusCode != 401 || !strings.Contains(body, "invalid_admin_key") || response.Header.Get("WWW-Authenticate") != "" {
		t.Fatal("Core admin rejection was replaced or added a browser Basic challenge")
	}
	r = adminRequest(t, server.URL, "GET", "/v1/agents", "separate-admin-key")
	response, body = responseBody(t, server, r)
	if response.StatusCode != 401 || response.Header.Get("WWW-Authenticate") != "" || calls.Load() != 2 || !strings.Contains(body, "invalid_admin_key") {
		t.Fatal("Core must reject the administrator bearer on project API routes")
	}
	r = consoleRequest(t, server, "GET", "/v1/agents")
	response, _ = responseBody(t, server, r)
	if response.StatusCode != 204 || calls.Load() != 3 {
		t.Fatal("Basic project request no longer uses the configured project credential")
	}
}

func TestSandboxNodeControlAndUnknownCoreRoutesAreNotProxied(t *testing.T) {
	var calls atomic.Int32
	server, _ := testConsole(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	for _, tc := range []struct{ method, path string }{
		{"POST", "/core/v1/sandbox/enroll"},
		{"GET", "/core/v1/sandbox/node/identity"},
		{"GET", "/core/v1/sandbox/node/connect"},
		{"GET", "/core/v1/sandbox/%6eode/identity"},
		{"GET", "/core/v1/sandbox"},
		{"GET", "/core"},
		{"GET", "/core/unknown"},
		{"POST", "/core/v1/sandbox/nodes"},
		{"GET", "/core/v1/sandbox/enrollment-tokens"},
		{"GET", "/core/v1/sandbox/nodes/id/allocations/extra"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := adminRequest(t, server.URL, tc.method, tc.path, "separate-admin-key")
			response, body := responseBody(t, server, r)
			if response.StatusCode != 404 || strings.Contains(body, "existing web build") {
				t.Fatalf("unsupported Core path = %d %q", response.StatusCode, body)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("node control or unknown Core route reached upstream")
	}
}

func TestSandboxAdminKeepsOriginAndPathBoundary(t *testing.T) {
	var calls atomic.Int32
	server, _ := testConsole(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
		status int
	}{
		{"cross origin", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") }, 403},
		{"cross-site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"wrong host", func(r *http.Request) { r.Host = "attacker.example" }, 403},
		{"missing provenance", func(r *http.Request) { r.Header.Del("Origin"); r.Header.Del("Sec-Fetch-Site") }, 403},
		{"unsafe path", func(r *http.Request) { r.URL.Path = "/core/v1/sandbox/../enroll" }, 400},
		{"upgrade", func(r *http.Request) { r.Header.Set("Upgrade", "websocket") }, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := adminRequest(t, server.URL, "POST", "/core/v1/sandbox/enrollment-tokens", "separate-admin-key")
			tc.change(r)
			response, _ := responseBody(t, server, r)
			if response.StatusCode != tc.status {
				t.Fatalf("status = %d", response.StatusCode)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("unsafe admin request reached Core")
	}
}

func TestConcurrentAdminAndProjectProxyRequestsDoNotShareCredentials(t *testing.T) {
	var mismatches atomic.Int32
	server, _ := testConsole(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "Bearer private-core-token"
		if strings.HasPrefix(r.URL.Path, "/core/") {
			want = "Bearer " + r.URL.Query().Get("request")
		}
		if r.Header.Get("Authorization") != want {
			mismatches.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	var group sync.WaitGroup
	for i := range 40 {
		for _, admin := range []bool{false, true} {
			group.Go(func() {
				r := consoleRequest(t, server, "GET", "/v1/agents")
				if admin {
					token := fmt.Sprintf("admin-%d", i)
					r = adminRequest(t, server.URL, "GET", "/core/v1/sandbox/nodes?request="+token, token)
				}
				response, _ := responseBody(t, server, r)
				if response.StatusCode != 204 {
					t.Errorf("concurrent status = %d", response.StatusCode)
				}
			})
		}
	}
	group.Wait()
	if mismatches.Load() != 0 {
		t.Fatalf("%d requests received another request's credential", mismatches.Load())
	}
}
