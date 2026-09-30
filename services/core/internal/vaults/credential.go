package vaults

import "time"

// Credential authentication types.
const (
	AuthStaticBearer = "static_bearer"
	AuthMCPOAuth     = "mcp_oauth"
)

// Credential contains only public metadata. Resource reads never select the
// sealed secret; only the bearer-token lookup for execution opens it.
type Credential struct {
	ID, VaultID, Name, AuthType, MCPServerURL string
	CreatedAt, UpdatedAt                      time.Time
	// OAuth is set for mcp_oauth Credentials only.
	OAuth *OAuthMetadata
}

type CredentialPage struct {
	Credentials []Credential
	NextCursor  string
}

// OAuthMetadata is the safe projection of a stored OAuth grant.
type OAuthMetadata struct {
	ExpiresAt *string               `json:"expires_at"`
	Refresh   *OAuthRefreshMetadata `json:"refresh"`
}

type OAuthRefreshMetadata struct {
	ClientID          string  `json:"client_id"`
	TokenEndpoint     string  `json:"token_endpoint"`
	TokenEndpointAuth string  `json:"token_endpoint_auth"`
	Resource          *string `json:"resource"`
	Scope             *string `json:"scope"`
}
