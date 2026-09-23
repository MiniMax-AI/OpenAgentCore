package v1

import "encoding/json"

// CredentialAuthInput represents the pinned create union. The API validates each
// discriminator separately so fields cannot cross authentication variants.
type CredentialAuthInput struct {
	Type         string                       `json:"type" binding:"required" enums:"static_bearer,mcp_oauth"`
	MCPServerURL *string                      `json:"mcp_server_url" binding:"required"`
	Token        *string                      `json:"token,omitempty" minLength:"1"`
	AccessToken  *string                      `json:"access_token,omitempty" minLength:"1"`
	ExpiresAt    *string                      `json:"expires_at,omitempty" extensions:"x-nullable"`
	Refresh      *OAuthCredentialRefreshInput `json:"refresh,omitempty" extensions:"x-nullable"`
}

type CreateCredentialRequest struct {
	Name *string              `json:"name" binding:"required"`
	Auth *CredentialAuthInput `json:"auth" binding:"required"`
}

type OAuthCredentialRefreshInput struct {
	ClientID          *string                 `json:"client_id" binding:"required"`
	RefreshToken      *string                 `json:"refresh_token" binding:"required"`
	TokenEndpoint     *string                 `json:"token_endpoint" binding:"required"`
	TokenEndpointAuth *OAuthEndpointAuthInput `json:"token_endpoint_auth" binding:"required"`
	Resource          *string                 `json:"resource,omitempty" extensions:"x-nullable"`
	Scope             *string                 `json:"scope,omitempty" extensions:"x-nullable"`
}

type OAuthEndpointAuthInput struct {
	Type         string  `json:"type" binding:"required" enums:"none,client_secret_basic,client_secret_post"`
	ClientSecret *string `json:"client_secret,omitempty"`
}

type UpdateCredentialRequest struct {
	Auth *CredentialAuthReplacement `json:"auth" binding:"required"`
}

// Raw nullable fields retain omission separately from explicit null.
type CredentialAuthReplacement struct {
	Type        string                             `json:"type" binding:"required" enums:"static_bearer,mcp_oauth"`
	Token       *string                            `json:"token,omitempty" minLength:"1"`
	AccessToken *string                            `json:"access_token,omitempty" extensions:"x-nullable" minLength:"1"`
	ExpiresAt   json.RawMessage                    `json:"expires_at,omitempty" swaggertype:"string" extensions:"x-nullable"`
	Refresh     *OAuthCredentialRefreshReplacement `json:"refresh,omitempty" extensions:"x-nullable"`
}

type OAuthCredentialRefreshReplacement struct {
	RefreshToken      *string                       `json:"refresh_token,omitempty" extensions:"x-nullable"`
	Scope             json.RawMessage               `json:"scope,omitempty" swaggertype:"string" extensions:"x-nullable"`
	TokenEndpointAuth *OAuthEndpointAuthReplacement `json:"token_endpoint_auth,omitempty" extensions:"x-nullable"`
}

type OAuthEndpointAuthReplacement struct {
	Type         string  `json:"type" binding:"required" enums:"client_secret_basic,client_secret_post"`
	ClientSecret *string `json:"client_secret,omitempty" extensions:"x-nullable"`
}

// CredentialAuth contains safe metadata only. OAuth nullable fields are emitted
// for OAuth credentials and excluded entirely from static bearer resources.
type CredentialAuth struct {
	Type         string                  `json:"type" binding:"required" enums:"static_bearer,mcp_oauth"`
	MCPServerURL string                  `json:"mcp_server_url" binding:"required"`
	ExpiresAt    *string                 `json:"expires_at" extensions:"x-nullable"`
	Refresh      *OAuthCredentialRefresh `json:"refresh" extensions:"x-nullable"`
}

type OAuthCredentialRefresh struct {
	ClientID          string            `json:"client_id" binding:"required"`
	TokenEndpoint     string            `json:"token_endpoint" binding:"required"`
	TokenEndpointAuth OAuthEndpointAuth `json:"token_endpoint_auth" binding:"required"`
	Resource          *string           `json:"resource" extensions:"x-nullable"`
	Scope             *string           `json:"scope" extensions:"x-nullable"`
}

type OAuthEndpointAuth struct {
	Type string `json:"type" binding:"required" enums:"none,client_secret_basic,client_secret_post"`
}

func (a CredentialAuth) MarshalJSON() ([]byte, error) {
	if a.Type == "static_bearer" {
		return json.Marshal(struct {
			Type         string `json:"type"`
			MCPServerURL string `json:"mcp_server_url"`
		}{a.Type, a.MCPServerURL})
	}
	type resource CredentialAuth
	return json.Marshal(resource(a))
}

type Credential struct {
	ID        string         `json:"id" binding:"required"`
	VaultID   string         `json:"vault_id" binding:"required"`
	Name      string         `json:"name" binding:"required"`
	Object    string         `json:"object" binding:"required" enums:"vault.credential"`
	Auth      CredentialAuth `json:"auth" binding:"required"`
	CreatedAt int64          `json:"created_at" binding:"required"`
	UpdatedAt int64          `json:"updated_at" binding:"required"`
}

type CredentialList struct {
	Object  string       `json:"object" binding:"required" enums:"list"`
	Data    []Credential `json:"data" binding:"required"`
	HasMore bool         `json:"has_more" binding:"required"`
	FirstID *string      `json:"first_id" extensions:"x-nullable"`
	LastID  *string      `json:"last_id" extensions:"x-nullable"`
}

type CredentialDeleted struct {
	ID      string `json:"id" binding:"required"`
	Deleted bool   `json:"deleted" binding:"required"`
	Object  string `json:"object" binding:"required" enums:"vault.credential.deleted"`
}
