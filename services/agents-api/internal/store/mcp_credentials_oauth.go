package store

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/oauthrefresh"
)

const oauthRefreshTimeout = 20 * time.Second

func (s *Store) oauthBearerToken(ctx context.Context, tenantID string, binding MCPCredentialBinding) (string, error) {
	// Bound both lock contention and the external exchange, so a slow provider cannot
	// hold a credential indefinitely. The HTTP client supplies its own tighter bound.
	ctx, cancel := context.WithTimeout(ctx, oauthRefreshTimeout)
	defer cancel()
	tx, credential, secret, err := s.lockOAuth(ctx, tenantID, binding.VaultID, binding.CredentialID, binding.ServerURL)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(context.Background())
	if credential.MCPServerURL != binding.ServerURL {
		return "", ErrNotFound
	}
	if secret.Metadata.ExpiresAt == nil {
		return oauthAccessToken(secret.AccessToken)
	}
	expiry, err := time.Parse(time.RFC3339Nano, *secret.Metadata.ExpiresAt)
	if err != nil {
		return "", errors.New("invalid OAuth token expiry")
	}
	if time.Now().Before(expiry) {
		return oauthAccessToken(secret.AccessToken)
	}
	refresh := secret.Metadata.Refresh
	if refresh == nil || secret.RefreshToken == "" || s.oauthRefresher == nil {
		return "", errors.New("expired OAuth credential cannot be refreshed")
	}
	token, err := s.oauthRefresher.Refresh(ctx, oauthrefresh.Request{
		TokenEndpoint: refresh.TokenEndpoint, ClientID: refresh.ClientID,
		AuthMethod: refresh.TokenEndpointAuth, ClientSecret: secret.ClientSecret,
		RefreshToken: secret.RefreshToken, Resource: refresh.Resource, Scope: refresh.Scope,
	})
	if err != nil {
		return "", errors.New("OAuth credential refresh failed")
	}
	if token.AccessToken == "" || token.ExpiresAt != nil && !time.Now().Before(*token.ExpiresAt) {
		return "", errors.New("OAuth refresh returned an unusable token")
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
	if _, err := s.saveOAuth(ctx, tx, tenantID, credential, secret); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", errors.New("OAuth credential refresh commit failed")
	}
	return token.AccessToken, nil
}

func oauthAccessToken(token string) (string, error) {
	if token == "" {
		return "", errors.New("OAuth access token is missing")
	}
	return token, nil
}
