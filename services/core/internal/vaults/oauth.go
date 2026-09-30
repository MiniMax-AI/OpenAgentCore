package vaults

import (
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/oauthrefresh"
)

// OAuthRefreshUpdate patches a stored refresh configuration. ScopeSet keeps
// omitted apart from null.
type OAuthRefreshUpdate struct {
	RefreshToken          *string
	Scope                 *string
	ScopeSet              bool
	TokenEndpointAuthType string
	ClientSecret          *string
}

// OAuthGrant is an mcp_oauth Credential's secret with the metadata it is
// refreshed by. Storage keeps the metadata readable and seals the whole grant,
// so the sealed copy authenticates every public setting used for refresh,
// including the token endpoint, and substituting stored metadata can never
// redirect a grant.
type OAuthGrant struct {
	Metadata     OAuthMetadata
	AccessToken  string
	RefreshToken string
	ClientSecret string
}

func validOAuthMetadata(metadata OAuthMetadata) bool {
	if metadata.ExpiresAt != nil {
		if _, err := time.Parse(time.RFC3339Nano, *metadata.ExpiresAt); err != nil {
			return false
		}
	}
	if refresh := metadata.Refresh; refresh != nil {
		if refresh.ClientID == "" || refresh.TokenEndpoint == "" {
			return false
		}
		switch refresh.TokenEndpointAuth {
		case "none", "client_secret_basic", "client_secret_post":
		default:
			return false
		}
	}
	return true
}

// validOAuthCreation checks a new grant: secrets that the refresh
// configuration cannot use are rejected rather than stored.
func validOAuthCreation(command CreateOAuthCredential) bool {
	if !validName(command.Name) || command.MCPServerURL == "" || !validOAuthMetadata(command.OAuth) {
		return false
	}
	refresh := command.OAuth.Refresh
	if refresh == nil {
		return command.RefreshToken == "" && command.ClientSecret == ""
	}
	return refresh.TokenEndpointAuth != "none" || command.ClientSecret == ""
}

// applyOAuthUpdate patches a stored grant. A new access token clears an
// omitted expiry. A refresh patch cannot add configuration or change the
// authentication method, and a client secret needs a method that uses one.
func applyOAuthUpdate(secret OAuthGrant, update UpdateOAuthCredential) (OAuthGrant, error) {
	if update.AccessToken != nil {
		secret.AccessToken = *update.AccessToken
		secret.Metadata.ExpiresAt = nil
	}
	if update.ExpiresAtSet {
		secret.Metadata.ExpiresAt = update.ExpiresAt
	}
	patch := update.Refresh
	if patch == nil {
		return secret, nil
	}
	if secret.Metadata.Refresh == nil {
		return OAuthGrant{}, ErrInvalidInput
	}
	refresh := *secret.Metadata.Refresh
	if patch.TokenEndpointAuthType != "" && patch.TokenEndpointAuthType != refresh.TokenEndpointAuth {
		return OAuthGrant{}, ErrInvalidInput
	}
	if patch.ClientSecret != nil {
		if refresh.TokenEndpointAuth == "none" {
			return OAuthGrant{}, ErrInvalidInput
		}
		secret.ClientSecret = *patch.ClientSecret
	}
	if patch.RefreshToken != nil {
		secret.RefreshToken = *patch.RefreshToken
	}
	if patch.ScopeSet {
		refresh.Scope = patch.Scope
	}
	secret.Metadata.Refresh = &refresh
	return secret, nil
}

// currentAccessToken returns the stored access token while it is usable at
// now. expired reports that the grant must be refreshed first; a grant without
// an expiry never expires.
func currentAccessToken(secret OAuthGrant, now time.Time) (token string, expired bool, err error) {
	if secret.Metadata.ExpiresAt != nil {
		expiry, err := time.Parse(time.RFC3339Nano, *secret.Metadata.ExpiresAt)
		if err != nil {
			return "", false, errors.New("invalid OAuth token expiry")
		}
		if !now.Before(expiry) {
			return "", true, nil
		}
	}
	if secret.AccessToken == "" {
		return "", false, errors.New("OAuth access token is missing")
	}
	return secret.AccessToken, false, nil
}

// refreshRequest is the exchange that renews an expired grant.
func refreshRequest(secret OAuthGrant) (oauthrefresh.Request, error) {
	refresh := secret.Metadata.Refresh
	if refresh == nil || secret.RefreshToken == "" {
		return oauthrefresh.Request{}, errors.New("expired OAuth credential cannot be refreshed")
	}
	return oauthrefresh.Request{
		TokenEndpoint: refresh.TokenEndpoint, ClientID: refresh.ClientID,
		AuthMethod: refresh.TokenEndpointAuth, ClientSecret: secret.ClientSecret,
		RefreshToken: secret.RefreshToken, Resource: refresh.Resource, Scope: refresh.Scope,
	}, nil
}

// applyRefreshedToken stores a refresh result that is usable at now. An
// omitted refresh token keeps the stored one.
func applyRefreshedToken(secret OAuthGrant, token oauthrefresh.Token, now time.Time) (OAuthGrant, error) {
	if token.AccessToken == "" || token.ExpiresAt != nil && !now.Before(*token.ExpiresAt) {
		return OAuthGrant{}, errors.New("OAuth refresh returned an unusable token")
	}
	secret.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		secret.RefreshToken = token.RefreshToken
	}
	secret.Metadata.ExpiresAt = nil
	if token.ExpiresAt != nil {
		value := token.ExpiresAt.UTC().Format(time.RFC3339Nano)
		secret.Metadata.ExpiresAt = &value
	}
	return secret, nil
}
