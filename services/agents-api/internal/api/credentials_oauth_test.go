package api

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func (f *credentialFixture) CreateOAuthCredential(_ context.Context, tenant, vault string, input store.CreateOAuthCredentialInput) (store.Credential, error) {
	f.tenant, f.vault, f.oauthInput, f.calls = tenant, vault, input, f.calls+1
	f.credential.Name, f.credential.MCPServerURL, f.credential.AuthType, f.credential.OAuth = input.Name, input.MCPServerURL, "mcp_oauth", &input.OAuth
	return f.credential, f.err
}

func (f *credentialFixture) UpdateOAuthCredential(_ context.Context, tenant, vault, id string, input store.UpdateOAuthCredentialInput) (store.Credential, error) {
	f.tenant, f.vault, f.id, f.oauthUpdate, f.calls = tenant, vault, id, input, f.calls+1
	return f.credential, f.err
}

func oauthCreateBody(t *testing.T, auth map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"name": " OAuth credential \n", "auth": auth})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestOAuthCredentialVariantsAndSafeResourceReads(t *testing.T) {
	for _, method := range []string{"", "none", "client_secret_basic", "client_secret_post"} {
		t.Run(method, func(t *testing.T) {
			h, f, tenant := credentialHandler(t)
			auth := map[string]any{"type": "mcp_oauth", "mcp_server_url": "https://mcp.example/tools", "access_token": "access-canary", "expires_at": nil, "refresh": nil}
			wantAuth := map[string]any{"type": "mcp_oauth", "mcp_server_url": "https://mcp.example/tools", "expires_at": nil, "refresh": nil}
			if method != "" {
				endpointAuth := map[string]any{"type": method}
				if method != "none" {
					endpointAuth["client_secret"] = "client-canary"
				}
				auth["refresh"] = map[string]any{"client_id": "test-client", "refresh_token": "refresh-canary", "token_endpoint": "https://issuer.example/token", "token_endpoint_auth": endpointAuth, "resource": nil, "scope": "read write"}
				wantAuth["refresh"] = map[string]any{"client_id": "test-client", "token_endpoint": "https://issuer.example/token", "token_endpoint_auth": map[string]any{"type": method}, "resource": nil, "scope": "read write"}
			}
			path := "/v1/vaults/" + f.credential.VaultID + "/credentials"
			w := credentialRequest(h, "POST", path, oauthCreateBody(t, auth))
			var body map[string]any
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || !reflect.DeepEqual(body["auth"], wantAuth) || strings.Contains(w.Body.String(), "canary") {
				t.Fatal("OAuth resource must expose only exact safe metadata", w.Code)
			}
			if f.tenant != tenant || f.vault != f.credential.VaultID || f.oauthInput.Name != "OAuth credential" || f.oauthInput.AccessToken != "access-canary" {
				t.Fatal("OAuth creation lost authenticated scope or secret input")
			}
			if method != "" && (f.oauthInput.RefreshToken != "refresh-canary" || f.oauthInput.OAuth.Refresh.TokenEndpointAuth != method) {
				t.Fatal("OAuth refresh input changed")
			}
			if method != "" && method != "none" && f.oauthInput.ClientSecret != "client-canary" {
				t.Fatal("OAuth client secret changed")
			}
			read := credentialRequest(h, "GET", path+"/"+f.credential.ID, "")
			if read.Code != 200 || read.Body.String() != w.Body.String() {
				t.Fatal("OAuth retrieval changed safe metadata")
			}
			f.page = store.CredentialPage{Credentials: []store.Credential{f.credential}}
			list := credentialRequest(h, "GET", path, "")
			var page struct {
				Data []map[string]any `json:"data"`
			}
			if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &page) != nil || len(page.Data) != 1 || !reflect.DeepEqual(page.Data[0], body) {
				t.Fatal("OAuth listing changed safe metadata")
			}
		})
	}
}

func TestOAuthCreateRejectsInvalidFieldsBeforeStorage(t *testing.T) {
	const base = `{"type":"mcp_oauth","mcp_server_url":"https://mcp.example","access_token":"access-canary"`
	const refresh = `,"refresh":{"client_id":"client","refresh_token":"refresh-canary","token_endpoint":"https://issuer.example/token","token_endpoint_auth":`
	for _, auth := range []string{
		`{"type":"mcp_oauth","mcp_server_url":"https://mcp.example"}`,
		`{"type":"mcp_oauth","mcp_server_url":"https://mcp.example","access_token":null}`,
		`{"type":"mcp_oauth","mcp_server_url":"http://mcp.example","access_token":"access-canary"}`,
		base + `,"token":"cross-variant"}`, base + `,"expires_at":3}`, base + `,"expires_at":"tomorrow"}`,
		base + `,"refresh":{}}`, base + `,"refresh":[]}`, base + `,"refresh":{"client_id":null}}`,
		base + refresh + `{"type":"none","client_secret":null}}}`,
		base + refresh + `{"type":"none","client_secret":"client-canary"}}}`,
		base + refresh + `{"type":"client_secret_basic"}}}`,
		base + refresh + `{"type":"client_secret_post","client_secret":null}}}`,
		base + refresh + `{"type":"unknown"}}}`,
		base + refresh + `{"type":"none"},"unknown":"refresh-canary"}}`,
		strings.Replace(base+refresh+`{"type":"none"}}}`, "https://issuer.example/token", "https://client-canary@issuer.example/token", 1),
	} {
		h, f, _ := credentialHandler(t)
		w := credentialRequest(h, "POST", "/v1/vaults/"+f.credential.VaultID+"/credentials", `{"name":"OAuth","auth":`+auth+`}`)
		if w.Code != 400 || f.calls != 0 || strings.Contains(w.Body.String(), "canary") {
			t.Fatal("invalid OAuth creation reached storage or exposed secrets", w.Code)
		}
	}
}

func TestOAuthUpdateRetainsPresenceAndSecretPointers(t *testing.T) {
	text := func(s string) *string { return &s }
	for _, tc := range []struct {
		auth string
		want store.UpdateOAuthCredentialInput
	}{
		{`{"type":"mcp_oauth"}`, store.UpdateOAuthCredentialInput{}},
		{`{"type":"mcp_oauth","access_token":null,"refresh":null}`, store.UpdateOAuthCredentialInput{}},
		{`{"type":"mcp_oauth","access_token":""}`, store.UpdateOAuthCredentialInput{AccessToken: text("")}},
		{`{"type":"mcp_oauth","expires_at":null}`, store.UpdateOAuthCredentialInput{ExpiresAtSet: true}},
		{`{"type":"mcp_oauth","expires_at":"2026-09-22T12:30:00.123+08:00"}`, store.UpdateOAuthCredentialInput{ExpiresAtSet: true, ExpiresAt: text("2026-09-22T12:30:00.123+08:00")}},
		{`{"type":"mcp_oauth","refresh":{}}`, store.UpdateOAuthCredentialInput{Refresh: &store.OAuthRefreshUpdate{}}},
		{`{"type":"mcp_oauth","refresh":{"refresh_token":null,"token_endpoint_auth":null}}`, store.UpdateOAuthCredentialInput{Refresh: &store.OAuthRefreshUpdate{}}},
		{`{"type":"mcp_oauth","refresh":{"scope":null}}`, store.UpdateOAuthCredentialInput{Refresh: &store.OAuthRefreshUpdate{ScopeSet: true}}},
		{`{"type":"mcp_oauth","refresh":{"scope":"","refresh_token":"refresh-canary","token_endpoint_auth":{"type":"client_secret_post","client_secret":"client-canary"}}}`, store.UpdateOAuthCredentialInput{Refresh: &store.OAuthRefreshUpdate{Scope: text(""), ScopeSet: true, RefreshToken: text("refresh-canary"), TokenEndpointAuthType: "client_secret_post", ClientSecret: text("client-canary")}}},
		{`{"type":"mcp_oauth","refresh":{"token_endpoint_auth":{"type":"client_secret_basic","client_secret":null}}}`, store.UpdateOAuthCredentialInput{Refresh: &store.OAuthRefreshUpdate{TokenEndpointAuthType: "client_secret_basic"}}},
	} {
		h, f, tenant := credentialHandler(t)
		f.credential.AuthType, f.credential.OAuth = "mcp_oauth", &store.OAuthMetadata{}
		w := credentialRequest(h, "POST", "/v1/vaults/"+f.credential.VaultID+"/credentials/"+f.credential.ID, `{"auth":`+tc.auth+`}`)
		if w.Code != 200 || f.tenant != tenant || f.vault != f.credential.VaultID || f.id != f.credential.ID || !reflect.DeepEqual(f.oauthUpdate, tc.want) || strings.Contains(w.Body.String(), "canary") {
			t.Fatal("OAuth update lost scope or nullable field intent", tc.auth, w.Code)
		}
	}
}

func TestOAuthUpdateRejectsInvalidPatchesBeforeStorage(t *testing.T) {
	for _, patch := range []string{
		`"token":"cross-variant"`, `"access_token":3`, `"expires_at":[]`, `"expires_at":"not a timestamp"`, `"expires_at":"2026-09-22T1:02:03Z"`, `"expires_at":"2026-09-22T01:02:03+24:00"`,
		`"refresh":{"client_id":"other"}`, `"refresh":{"token_endpoint":"https://other.example"}`,
		`"refresh":{"resource":null}`, `"refresh":{"scope":3}`, `"refresh":{"refresh_token":false}`,
		`"refresh":{"token_endpoint_auth":{}}`, `"refresh":{"token_endpoint_auth":{"type":"none"}}`,
		`"refresh":{"token_endpoint_auth":{"type":"client_secret_basic","client_secret":3}}`,
		`"refresh":{"token_endpoint_auth":{"type":"client_secret_post","client_id":"other"}}`,
	} {
		h, f, _ := credentialHandler(t)
		w := credentialRequest(h, "POST", "/v1/vaults/"+f.credential.VaultID+"/credentials/"+f.credential.ID, `{"auth":{"type":"mcp_oauth",`+patch+`}}`)
		if w.Code != 400 || f.calls != 0 {
			t.Fatal("invalid OAuth patch reached storage", patch, w.Code)
		}
	}
}

func TestOAuthCredentialStoreFailuresUseSafeExistingErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{{store.ErrNotFound, 404}, {store.ErrInvalidInput, 400}, {store.ErrCredentialStorageUnavailable, 503}, {errors.New("access-canary"), 500}} {
		for _, update := range []bool{false, true} {
			h, f, _ := credentialHandler(t)
			f.err = tc.err
			path, body := "/v1/vaults/"+f.credential.VaultID+"/credentials", `{"name":"OAuth","auth":{"type":"mcp_oauth","mcp_server_url":"https://mcp.example","access_token":"access-canary"}}`
			if update {
				path += "/" + f.credential.ID
				body = `{"auth":{"type":"mcp_oauth","access_token":"access-canary"}}`
			}
			w := credentialRequest(h, "POST", path, body)
			if w.Code != tc.code || strings.Contains(w.Body.String(), "canary") {
				t.Fatal("unsafe OAuth store error", w.Code)
			}
		}
	}
}
