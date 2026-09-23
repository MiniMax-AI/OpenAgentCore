package runtimeenrollment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type ConnectionStore interface {
	AuthenticateEnvironmentExecutor(context.Context, string, string) (string, error)
	GetEnvironment(context.Context, string, string) (store.Environment, error)
	GetSessionDevice(context.Context, string, string) (store.ExecutionDevice, error)
	GetDeviceCredential(context.Context, string) (device.Credential, bool, error)
}

// ConnectionHandler observes an existing binding without enrollment or execution.
// Executor authority never grants access to the public Session API.
func ConnectionHandler(s ConnectionStore, registry *gateway.Registry) http.Handler {
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
		digest := device.HashCredential(authorization[1])
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		connected, err := runtimeConnected(ctx, s, registry, environment, digest)
		switch {
		case errors.Is(err, store.ErrNotFound):
			fail(http.StatusUnauthorized)
		case errors.Is(err, store.ErrDeviceBindingConflict):
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

func runtimeConnected(ctx context.Context, s ConnectionStore, registry *gateway.Registry, environment, digest string) (bool, error) {
	tenant, err := s.AuthenticateEnvironmentExecutor(ctx, environment, digest)
	if err != nil {
		return false, err
	}
	current, err := s.GetEnvironment(ctx, tenant, environment)
	if err != nil {
		return false, err
	}
	if current.Status == "failed" || current.Status == "expired" {
		return false, store.ErrNotFound
	}
	bound, err := s.GetSessionDevice(ctx, tenant, current.SessionID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if bound.EnvironmentID != environment {
		return false, store.ErrDeviceBindingConflict
	}
	credential, found, err := s.GetDeviceCredential(ctx, bound.ID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, store.ErrNotFound
	}
	if credential.CredentialHash != digest {
		return false, store.ErrDeviceBindingConflict
	}
	peer, err := registry.LookupDevice(bound.ID)
	if errors.Is(err, gateway.ErrDeviceNotRegistered) {
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
	return !peer.IsClosed(), nil
}
