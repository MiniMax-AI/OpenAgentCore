package v1

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCredentialAuthUnionNullableProjection(t *testing.T) {
	for _, tc := range []struct {
		auth CredentialAuth
		want string
	}{
		{CredentialAuth{Type: "static_bearer", MCPServerURL: "https://mcp.example", Refresh: &OAuthCredentialRefresh{ClientID: "must-not-cross-variants"}}, `{"type":"static_bearer","mcp_server_url":"https://mcp.example"}`},
		{CredentialAuth{Type: "mcp_oauth", MCPServerURL: "https://mcp.example"}, `{"type":"mcp_oauth","mcp_server_url":"https://mcp.example","expires_at":null,"refresh":null}`},
		{CredentialAuth{Type: "mcp_oauth", MCPServerURL: "https://mcp.example", Refresh: &OAuthCredentialRefresh{ClientID: "client", TokenEndpoint: "https://issuer.example/token", TokenEndpointAuth: OAuthEndpointAuth{Type: "none"}}}, `{"type":"mcp_oauth","mcp_server_url":"https://mcp.example","expires_at":null,"refresh":{"client_id":"client","token_endpoint":"https://issuer.example/token","token_endpoint_auth":{"type":"none"},"resource":null,"scope":null}}`},
	} {
		raw, err := json.Marshal(tc.auth)
		if err != nil {
			t.Fatal(err)
		}
		var got, want map[string]any
		if json.Unmarshal(raw, &got) != nil || json.Unmarshal([]byte(tc.want), &want) != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("credential discriminator or nullable metadata changed", string(raw))
		}
	}
}
