package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/oauthrefresh"
)

const oauthRefreshTimeout = 20 * time.Second

func (s *Store) oauthBearerToken(ctx context.Context, tenantID string, binding MCPCredentialBinding) (string, error) {
	// Bound both lock contention and the external exchange, so a slow provider cannot
	// hold a credential indefinitely. The HTTP client supplies its own tighter bound.
	ctx, cancel := context.WithTimeout(ctx, oauthRefreshTimeout)
	defer cancel()
	var bearer string
	err := s.withOAuth(ctx, tenantID, binding.VaultID, binding.CredentialID, binding.ServerURL, "OAuth credential refresh commit failed", func(ctx context.Context, tx pgx.Tx, credential Credential, secret oauthSecret) error {
		if credential.MCPServerURL != binding.ServerURL {
			return ErrNotFound
		}
		if secret.Metadata.ExpiresAt == nil {
			var err error
			bearer, err = oauthAccessToken(secret.AccessToken)
			return err
		}
		expiry, err := time.Parse(time.RFC3339Nano, *secret.Metadata.ExpiresAt)
		if err != nil {
			return errors.New("invalid OAuth token expiry")
		}
		if time.Now().Before(expiry) {
			bearer, err = oauthAccessToken(secret.AccessToken)
			return err
		}
		refresh := secret.Metadata.Refresh
		if refresh == nil || secret.RefreshToken == "" || s.oauthRefresher == nil {
			return errors.New("expired OAuth credential cannot be refreshed")
		}
		token, err := s.oauthRefresher.Refresh(ctx, oauthrefresh.Request{
			TokenEndpoint: refresh.TokenEndpoint, ClientID: refresh.ClientID,
			AuthMethod: refresh.TokenEndpointAuth, ClientSecret: secret.ClientSecret,
			RefreshToken: secret.RefreshToken, Resource: refresh.Resource, Scope: refresh.Scope,
		})
		if err != nil {
			return errors.New("OAuth credential refresh failed")
		}
		if token.AccessToken == "" || token.ExpiresAt != nil && !time.Now().Before(*token.ExpiresAt) {
			return errors.New("OAuth refresh returned an unusable token")
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
			return err
		}
		bearer = token.AccessToken
		return nil
	})
	if err != nil {
		return "", err
	}
	return bearer, nil
}

func oauthAccessToken(token string) (string, error) {
	if token == "" {
		return "", errors.New("OAuth access token is missing")
	}
	return token, nil
}
