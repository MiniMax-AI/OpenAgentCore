package runtimeenrollment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

type ConnectionStore interface {
	AuthenticateEnvironmentExecutor(context.Context, string, string) (string, error)
	GetEnvironment(context.Context, string, string) (sessions.Environment, error)
	GetSessionDevice(context.Context, string, string) (sessions.ExecutionDevice, error)
	GetDeviceCredential(context.Context, string) (runtimedevice.Credential, bool, error)
}

// ConnectionHandler observes an existing binding without enrollment or execution.
// Executor authority never grants access to the public Session API.
func ConnectionHandler(s ConnectionStore, registry *runtimegateway.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		fail := func(status int) { http.Error(w, http.StatusText(status), status) }
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			fail(http.StatusMethodNotAllowed)
			return
		}
		authorization := strings.Fields(r.Header.Get("Authorization"))
		if len(authorization) != 2 || !strings.EqualFold(authorization[0], "Bearer") {
			fail(http.StatusUnauthorized)
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query) != 1 || len(query["environment_id"]) != 1 || query.Get("environment_id") == "" {
			fail(http.StatusBadRequest)
			return
		}
		environment := query.Get("environment_id")
		digest := runtimedevice.HashCredential(authorization[1])
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		connected, err := RuntimeConnected(ctx, s, registry, environment, digest)
		switch {
		case errors.Is(err, sessions.ErrNotFound):
			fail(http.StatusUnauthorized)
		case errors.Is(err, sessions.ErrDeviceBindingConflict):
			fail(http.StatusConflict)
		case err != nil:
			fail(http.StatusServiceUnavailable)
		default:
			status := "disconnected"
			if connected {
				status = "connected"
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				EnvironmentID string `json:"environment_id"`
				Status        string `json:"status"`
			}{environment, status})
		}
	})
}

// RuntimeConnected observes current executor authority and a matching open peer.
// It rechecks authority after the peer; callers must not supply a stale transaction.
func RuntimeConnected(ctx context.Context, s ConnectionStore, registry *runtimegateway.Registry, environment, digest string) (bool, error) {
	tenant, err := s.AuthenticateEnvironmentExecutor(ctx, environment, digest)
	if err != nil {
		return false, err
	}
	current, err := s.GetEnvironment(ctx, tenant, environment)
	if err != nil {
		return false, err
	}
	if current.Status == "failed" || current.Status == "expired" {
		return false, sessions.ErrNotFound
	}
	bound, err := s.GetSessionDevice(ctx, tenant, current.SessionID)
	if errors.Is(err, sessions.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if bound.EnvironmentID != environment {
		return false, sessions.ErrDeviceBindingConflict
	}
	credential, found, err := s.GetDeviceCredential(ctx, bound.ID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, sessions.ErrNotFound
	}
	if credential.CredentialHash != digest {
		return false, sessions.ErrDeviceBindingConflict
	}
	if registry == nil {
		return false, nil
	}
	peer, err := registry.LookupDevice(bound.ID)
	if errors.Is(err, runtimegateway.ErrDeviceNotRegistered) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if current.Status != "connected" || peer.IsClosed() || !peer.AuthenticatedWith(digest) {
		return false, nil
	}
	// Recheck authority after reading the socket; rotation/revocation never inherits
	// the connected observation of a socket authenticated with the former key.
	if _, err = s.AuthenticateEnvironmentExecutor(ctx, environment, digest); err != nil {
		return false, err
	}
	// The executor key can remain valid while the device itself is revoked.
	// Recheck the shared authority view too, including Environment retirement.
	credential, found, err = s.GetDeviceCredential(ctx, bound.ID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, sessions.ErrNotFound
	}
	if credential.CredentialHash != digest {
		return false, sessions.ErrDeviceBindingConflict
	}
	return !peer.IsClosed() && peer.AuthenticatedWith(digest), nil
}
