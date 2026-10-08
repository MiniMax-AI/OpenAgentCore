package runtimeenrollment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

type ConnectionStore interface {
	AuthenticateEnvironmentExecutor(context.Context, string, string) (string, error)
	GetEnvironment(context.Context, string, string) (sessions.Environment, error)
	GetEnvironmentResource(context.Context, string, string) (runtimedevice.ServeAuthority, error)
}

// ConnectionHandler observes an existing enrollment without enrollment or
// execution. Executor authority never grants access to the public Session API.
func ConnectionHandler(s ConnectionStore, links *relay.Relay) http.Handler {
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
		connected, err := RuntimeConnected(ctx, s, links, environment, digest)
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

// RuntimeConnected reports whether the sandbox the executor credential
// enrolled serves the Environment: the credential authenticates for the
// Environment, the Environment's live Link resource is that enrollment with
// the same credential, the relay holds its serve peer, and the credential
// still has that authority afterwards. A live resource of another credential
// is ErrDeviceBindingConflict.
func RuntimeConnected(ctx context.Context, s ConnectionStore, links *relay.Relay, environment, digest string) (bool, error) {
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
	resource, found, err := enrolledResource(ctx, s, tenant, environment, digest)
	if err != nil || !found || !links.Serving(resource.Ref()) {
		return false, err
	}
	// Recheck authority after the relay; rotation or revocation never
	// inherits the serve peer of the former credential.
	if _, err = s.AuthenticateEnvironmentExecutor(ctx, environment, digest); err != nil {
		return false, err
	}
	again, found, err := enrolledResource(ctx, s, tenant, environment, digest)
	return err == nil && found && again == resource && links.Serving(resource.Ref()), err
}

// enrolledResource reads the Environment's live Link resource and reports
// whether it has one; the resource must be an enrollment served with the
// credential.
func enrolledResource(ctx context.Context, s ConnectionStore, tenant, environment, digest string) (sandboxbootstrap.Resource, bool, error) {
	authority, err := s.GetEnvironmentResource(ctx, tenant, environment)
	if errors.Is(err, sessions.ErrNotFound) {
		return sandboxbootstrap.Resource{}, false, nil
	}
	if err != nil {
		return sandboxbootstrap.Resource{}, false, err
	}
	if authority.Resource.Kind != "enrollment" || authority.CredentialHash != digest {
		return sandboxbootstrap.Resource{}, false, sessions.ErrDeviceBindingConflict
	}
	return authority.Resource, true, nil
}
