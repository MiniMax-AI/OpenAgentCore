package oauthrefresh

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshGrantAndAuthentication(t *testing.T) {
	for _, method := range []string{"none", "client_secret_basic", "client_secret_post"} {
		t.Run(method, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" || r.ParseForm() != nil {
					t.Error("invalid token request")
				}
				if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-old" || r.Form.Get("scope") != "read write" || r.Form.Get("resource") != "https://mcp.example/tool?a=1&b=2" {
					t.Error("grant fields missing or changed")
				}
				if method == "client_secret_basic" {
					id, secret, ok := r.BasicAuth()
					if !ok || id != url.QueryEscape("client +id") || secret != url.QueryEscape("secret &+=") || r.Form.Has("client_secret") || r.Form.Has("client_id") {
						t.Error("incorrect basic authentication")
					}
				} else {
					if r.Header.Get("Authorization") != "" || r.Form.Get("client_id") != "client +id" {
						t.Error("unexpected header or client")
					}
					if method == "none" && r.Form.Has("client_secret") {
						t.Error("public client sent a secret")
					}
					if method == "client_secret_post" && r.Form.Get("client_secret") != "secret &+=" {
						t.Error("missing client secret")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"access_token": "access-new", "refresh_token": "refresh-new", "token_type": "Bearer", "expires_in": 300})
			}))
			defer server.Close()
			client := trustedClient(t, server)
			scope, resource := "read write", "https://mcp.example/tool?a=1&b=2"
			input := Request{TokenEndpoint: server.URL, ClientID: "client +id", AuthMethod: method, RefreshToken: "refresh-old", Scope: &scope, Resource: &resource}
			if method != "none" {
				input.ClientSecret = "secret &+="
			}
			token, err := client.Refresh(context.Background(), input)
			if err != nil || token.AccessToken != "access-new" || token.RefreshToken != "refresh-new" || token.ExpiresAt == nil || !token.ExpiresAt.After(time.Now().Add(250*time.Second)) || calls.Load() != 1 {
				t.Fatal("refresh did not preserve the complete grant", token.ExpiresAt, calls.Load(), err)
			}
		})
	}
}

func trustedClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := NewClient([]string{server.URL})
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client.transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	t.Cleanup(client.transport.CloseIdleConnections)
	return client
}

func TestRefreshFailuresAreSecretSafeAndDoNotRetry(t *testing.T) {
	for _, mode := range []string{"invalid_grant", "missing_token", "wrong_type", "malformed", "oversized", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "invalid_grant":
					w.WriteHeader(400)
					w.Write([]byte(`{"error":"invalid_grant","error_description":"DO-NOT-LEAK-SECRET"}`))
				case "missing_token":
					w.Write([]byte(`{"token_type":"Bearer"}`))
				case "wrong_type":
					w.Write([]byte(`{"access_token":"DO-NOT-LEAK-SECRET","token_type":"mac"}`))
				case "malformed":
					w.Write([]byte("DO-NOT-LEAK-SECRET"))
				case "oversized":
					w.Write([]byte(`{"access_token":"` + strings.Repeat("x", 1<<20) + `","token_type":"Bearer"}`))
				case "redirect":
					w.Header().Set("Location", "/stolen")
					w.WriteHeader(307)
				}
			}))
			defer server.Close()
			client := trustedClient(t, server)
			_, err := client.Refresh(context.Background(), Request{TokenEndpoint: server.URL, ClientID: "client", AuthMethod: "client_secret_basic", ClientSecret: "DO-NOT-LEAK-SECRET", RefreshToken: "refresh"})
			if err != ErrRefresh || calls.Load() != 1 {
				t.Fatal("failure leaked details or retried", err, calls.Load())
			}
		})
	}
}

func TestRefreshRetainsTokenAndHonorsCancellation(t *testing.T) {
	var block atomic.Bool
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if block.Load() {
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"next","token_type":"bearer"}`))
	}))
	defer server.Close()
	defer close(release)
	client := trustedClient(t, server)
	input := Request{TokenEndpoint: server.URL, AuthMethod: "none", ClientID: "public", RefreshToken: "retain"}
	token, err := client.Refresh(context.Background(), input)
	if err != nil || token.RefreshToken != "retain" || token.ExpiresAt != nil {
		t.Fatal("omitted grant fields changed", err)
	}
	block.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := client.Refresh(ctx, input); err != ErrRefresh {
		t.Fatal(err)
	}
}

func TestPrivateOriginDoesNotDisableTLSVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted certificate reached provider") }))
	defer server.Close()
	client, err := NewClient([]string{server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer client.transport.CloseIdleConnections()
	if _, err := client.Refresh(context.Background(), Request{TokenEndpoint: server.URL, AuthMethod: "none", RefreshToken: "secret"}); err != ErrRefresh {
		t.Fatal(err)
	}
}
