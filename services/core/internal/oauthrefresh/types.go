// Package oauthrefresh exchanges a stored OAuth refresh grant for an access token.
package oauthrefresh

import (
	"context"
	"time"
)

// Request is private execution input. Never log it or return it as metadata.
type Request struct {
	TokenEndpoint, ClientID, AuthMethod, ClientSecret, RefreshToken string
	Resource, Scope                                                 *string
}

// Token contains only the new grant material returned by the token endpoint.
// An omitted RefreshToken leaves the stored refresh token unchanged.
type Token struct {
	AccessToken, RefreshToken string
	ExpiresAt                 *time.Time
}

// Refresher is the narrow network boundary used by credential persistence.
type Refresher interface {
	Refresh(context.Context, Request) (Token, error)
}
