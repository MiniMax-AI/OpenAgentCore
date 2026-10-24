package api

import (
	"cmp"
	"encoding/json"
	"net/url"
	"regexp"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

// The helpers below check the Core-local rules that the pinned schema does not
// express; each names its field.
func nonEmptyString(path, value string) error {
	if value == "" {
		return &fieldError{param: path, message: path + " must be a nonempty string."}
	}
	return nil
}

func httpsURL(path, value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return &fieldError{param: path, message: path + " must be an absolute HTTPS URL without userinfo or a fragment."}
	}
	return nil
}

var credentialTimestamp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$`)

func credentialExpiry(path string, value *string) error {
	if value != nil {
		if _, err := time.Parse(time.RFC3339Nano, *value); err != nil || !credentialTimestamp.MatchString(*value) {
			return &fieldError{param: path, message: path + " must be an RFC 3339 timestamp."}
		}
	}
	return nil
}

// oauthCredentialCreate parses an mcp_oauth creation whose shape is checked;
// the caller sets the tenant and Vault.
func oauthCredentialCreate(auth v1.CredentialAuthInput, name string) (vaults.CreateOAuthCredential, error) {
	input := vaults.CreateOAuthCredential{Name: name, MCPServerURL: *auth.MCPServerURL, AccessToken: *auth.AccessToken}
	input.OAuth.ExpiresAt = auth.ExpiresAt
	err := cmp.Or(httpsURL("auth.mcp_server_url", input.MCPServerURL), nonEmptyString("auth.access_token", input.AccessToken), credentialExpiry("auth.expires_at", auth.ExpiresAt))
	if r := auth.Refresh; err == nil && r != nil {
		a := r.TokenEndpointAuth
		if a.ClientSecret != nil {
			input.ClientSecret = *a.ClientSecret
		}
		input.RefreshToken = *r.RefreshToken
		input.OAuth.Refresh = &vaults.OAuthRefreshMetadata{ClientID: *r.ClientID, TokenEndpoint: *r.TokenEndpoint, TokenEndpointAuth: a.Type, Resource: r.Resource, Scope: r.Scope}
		err = httpsURL("auth.refresh.token_endpoint", *r.TokenEndpoint)
	}
	return input, err
}

// oauthCredentialUpdate parses an mcp_oauth patch whose shape is checked; the
// caller sets the Credential's identity.
func oauthCredentialUpdate(auth v1.CredentialAuthReplacement) (vaults.UpdateOAuthCredential, error) {
	input := vaults.UpdateOAuthCredential{AccessToken: auth.AccessToken}
	var err error
	if input.AccessToken != nil {
		err = nonEmptyString("auth.access_token", *input.AccessToken)
	}
	if len(auth.ExpiresAt) > 0 {
		input.ExpiresAtSet = true
		_ = json.Unmarshal(auth.ExpiresAt, &input.ExpiresAt)
		err = cmp.Or(err, credentialExpiry("auth.expires_at", input.ExpiresAt))
	}
	if r := auth.Refresh; r != nil {
		input.Refresh = &vaults.OAuthRefreshUpdate{RefreshToken: r.RefreshToken}
		if len(r.Scope) > 0 {
			input.Refresh.ScopeSet = true
			_ = json.Unmarshal(r.Scope, &input.Refresh.Scope)
		}
		if a := r.TokenEndpointAuth; a != nil {
			input.Refresh.TokenEndpointAuthType, input.Refresh.ClientSecret = a.Type, a.ClientSecret
		}
	}
	if err == nil && input.AccessToken == nil && !input.ExpiresAtSet && (input.Refresh == nil ||
		(input.Refresh.RefreshToken == nil && input.Refresh.ClientSecret == nil && !input.Refresh.ScopeSet)) {
		err = &fieldError{param: "auth", message: "auth requires access_token, expires_at or refresh to update."}
	}
	return input, err
}
