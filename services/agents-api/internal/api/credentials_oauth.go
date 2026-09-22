package api

import (
	"encoding/json"
	"net/url"
	"regexp"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
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

func credentialAuthType(raw json.RawMessage) string {
	var value struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value.Type
}

func oauthCredentialCreate(raw json.RawMessage, name string) (store.CreateOAuthCredentialInput, error) {
	var auth v1.CredentialAuthInput
	input := store.CreateOAuthCredentialInput{Name: name}
	if decodeInputObject(raw, &auth, "type", "mcp_server_url", "access_token", "expires_at", "refresh") != nil || auth.Type != "mcp_oauth" || auth.AccessToken == nil || !credentialHTTPSURL(auth.MCPServerURL) || !credentialExpiry(auth.ExpiresAt) {
		return input, store.ErrInvalidInput
	}
	input.MCPServerURL, input.AccessToken = *auth.MCPServerURL, *auth.AccessToken
	input.OAuth.ExpiresAt = auth.ExpiresAt
	if r := auth.Refresh; r != nil {
		if r.ClientID == nil || r.RefreshToken == nil || !credentialHTTPSURL(r.TokenEndpoint) || r.TokenEndpointAuth == nil {
			return input, store.ErrInvalidInput
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
				return input, store.ErrInvalidInput
			}
		case "client_secret_basic", "client_secret_post":
			if a.ClientSecret == nil {
				return input, store.ErrInvalidInput
			}
			input.ClientSecret = *a.ClientSecret
		default:
			return input, store.ErrInvalidInput
		}
		input.RefreshToken = *r.RefreshToken
		input.OAuth.Refresh = &store.OAuthRefreshMetadata{ClientID: *r.ClientID, TokenEndpoint: *r.TokenEndpoint, TokenEndpointAuth: a.Type, Resource: r.Resource, Scope: r.Scope}
	}
	return input, nil
}

func oauthCredentialUpdate(raw json.RawMessage) (store.UpdateOAuthCredentialInput, error) {
	var auth v1.CredentialAuthReplacement
	var input store.UpdateOAuthCredentialInput
	if decodeInputObject(raw, &auth, "type", "access_token", "expires_at", "refresh") != nil || auth.Type != "mcp_oauth" {
		return input, store.ErrInvalidInput
	}
	input.AccessToken = auth.AccessToken
	if len(auth.ExpiresAt) > 0 {
		input.ExpiresAtSet = true
		if json.Unmarshal(auth.ExpiresAt, &input.ExpiresAt) != nil || !credentialExpiry(input.ExpiresAt) {
			return input, store.ErrInvalidInput
		}
	}
	if r := auth.Refresh; r != nil {
		input.Refresh = &store.OAuthRefreshUpdate{RefreshToken: r.RefreshToken}
		if len(r.Scope) > 0 {
			input.Refresh.ScopeSet = true
			if json.Unmarshal(r.Scope, &input.Refresh.Scope) != nil {
				return input, store.ErrInvalidInput
			}
		}
		if a := r.TokenEndpointAuth; a != nil {
			if a.Type != "client_secret_basic" && a.Type != "client_secret_post" {
				return input, store.ErrInvalidInput
			}
			input.Refresh.TokenEndpointAuthType, input.Refresh.ClientSecret = a.Type, a.ClientSecret
		}
	}
	return input, nil
}
