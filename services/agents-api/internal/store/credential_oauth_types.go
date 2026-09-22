package store

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

// CreateOAuthCredentialInput keeps write-only secrets separate from metadata.
type CreateOAuthCredentialInput struct {
	Name, MCPServerURL, AccessToken string
	OAuth                           OAuthMetadata
	RefreshToken, ClientSecret      string
}

// ExpiresAtSet and ScopeSet preserve omitted-versus-null update semantics.
type UpdateOAuthCredentialInput struct {
	AccessToken  *string
	ExpiresAt    *string
	ExpiresAtSet bool
	Refresh      *OAuthRefreshUpdate
}

type OAuthRefreshUpdate struct {
	RefreshToken          *string
	Scope                 *string
	ScopeSet              bool
	TokenEndpointAuthType string
	ClientSecret          *string
}
