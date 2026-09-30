package api

import (
	"encoding/json"
	"net/url"
	"regexp"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

func credentialHTTPSURL(value *string) bool {
	if value == nil {
		return false
	}
	u, err := url.Parse(*value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

var credentialTimestamp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$`)

func credentialExpiry(value *string) bool {
	if value == nil {
		return true
	}
	if !credentialTimestamp.MatchString(*value) {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, *value)
	return err == nil
}

// credentialAuthType reads the exact type member; a case variant is unknown.
func credentialAuthType(raw json.RawMessage) string {
	var fields map[string]json.RawMessage
	var value string
	if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(fields["type"], &value) != nil {
		return ""
	}
	return value
}

// oauthCredentialCreate parses an mcp_oauth creation; the caller sets the
// tenant and Vault.
func oauthCredentialCreate(raw json.RawMessage, name string) (vaults.CreateOAuthCredential, error) {
	var auth v1.CredentialAuthInput
	input := vaults.CreateOAuthCredential{Name: name}
	if decodeInputObject(raw, &auth, "type", "mcp_server_url", "access_token", "expires_at", "refresh") != nil || auth.Type != "mcp_oauth" || auth.AccessToken == nil || *auth.AccessToken == "" || !credentialHTTPSURL(auth.MCPServerURL) || !credentialExpiry(auth.ExpiresAt) {
		return input, vaults.ErrInvalidInput
	}
	input.MCPServerURL, input.AccessToken = *auth.MCPServerURL, *auth.AccessToken
	input.OAuth.ExpiresAt = auth.ExpiresAt
	if r := auth.Refresh; r != nil {
		if r.ClientID == nil || r.RefreshToken == nil || !credentialHTTPSURL(r.TokenEndpoint) || r.TokenEndpointAuth == nil {
			return input, vaults.ErrInvalidInput
		}
		a := r.TokenEndpointAuth
		switch a.Type {
		case "none":
			// Presence of client_secret is invalid even when explicitly null.
			var fields struct {
				Refresh struct {
					Auth json.RawMessage `json:"token_endpoint_auth"`
				} `json:"refresh"`
			}
			if json.Unmarshal(raw, &fields) != nil || decodeInputObject(fields.Refresh.Auth, &struct {
				Type string `json:"type"`
			}{}, "type") != nil {
				return input, vaults.ErrInvalidInput
			}
		case "client_secret_basic", "client_secret_post":
			if a.ClientSecret == nil {
				return input, vaults.ErrInvalidInput
			}
			input.ClientSecret = *a.ClientSecret
		default:
			return input, vaults.ErrInvalidInput
		}
		input.RefreshToken = *r.RefreshToken
		input.OAuth.Refresh = &vaults.OAuthRefreshMetadata{ClientID: *r.ClientID, TokenEndpoint: *r.TokenEndpoint, TokenEndpointAuth: a.Type, Resource: r.Resource, Scope: r.Scope}
	}
	return input, nil
}

// oauthCredentialUpdate parses an mcp_oauth patch; the caller sets the
// Credential's identity.
func oauthCredentialUpdate(raw json.RawMessage) (vaults.UpdateOAuthCredential, error) {
	var auth v1.CredentialAuthReplacement
	var input vaults.UpdateOAuthCredential
	if decodeInputObject(raw, &auth, "type", "access_token", "expires_at", "refresh") != nil || auth.Type != "mcp_oauth" {
		return input, vaults.ErrInvalidInput
	}
	input.AccessToken = auth.AccessToken
	if input.AccessToken != nil && *input.AccessToken == "" {
		return input, vaults.ErrInvalidInput
	}
	if len(auth.ExpiresAt) > 0 {
		input.ExpiresAtSet = true
		if json.Unmarshal(auth.ExpiresAt, &input.ExpiresAt) != nil || !credentialExpiry(input.ExpiresAt) {
			return input, vaults.ErrInvalidInput
		}
	}
	if r := auth.Refresh; r != nil {
		input.Refresh = &vaults.OAuthRefreshUpdate{RefreshToken: r.RefreshToken}
		if len(r.Scope) > 0 {
			input.Refresh.ScopeSet = true
			if json.Unmarshal(r.Scope, &input.Refresh.Scope) != nil {
				return input, vaults.ErrInvalidInput
			}
		}
		if a := r.TokenEndpointAuth; a != nil {
			if a.Type != "client_secret_basic" && a.Type != "client_secret_post" {
				return input, vaults.ErrInvalidInput
			}
			input.Refresh.TokenEndpointAuthType, input.Refresh.ClientSecret = a.Type, a.ClientSecret
		}
	}
	if input.AccessToken == nil && !input.ExpiresAtSet && (input.Refresh == nil ||
		(input.Refresh.RefreshToken == nil && input.Refresh.ClientSecret == nil && !input.Refresh.ScopeSet)) {
		return input, vaults.ErrInvalidInput
	}
	return input, nil
}
