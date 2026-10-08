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

// Connections observes enrolled sandboxes through their live Link resources.
type Connections struct {
	Store ConnectionStore
	Links *relay.Relay
}

// ServeHTTP observes an existing enrollment without enrollment or execution.
// Executor authority never grants access to the public Session API.
func (c *Connections) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	connected, err := c.ExecutorConnected(ctx, environment, digest)
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
}

// ExecutorConnected reports whether the sandbox the executor credential
// enrolled serves the Environment: the credential authenticates for the
// Environment, the Environment's live Link resource is that enrollment with
// the same credential, the relay holds its serve peer, and the credential
// still has that authority afterwards. A live resource of another credential
// is ErrDeviceBindingConflict.
func (c *Connections) ExecutorConnected(ctx context.Context, environment, digest string) (bool, error) {
	tenant, err := c.Store.AuthenticateEnvironmentExecutor(ctx, environment, digest)
	if err != nil {
		return false, err
	}
	current, err := c.Store.GetEnvironment(ctx, tenant, environment)
	if err != nil {
		return false, err
	}
	if current.Status == "failed" || current.Status == "expired" {
		return false, sessions.ErrNotFound
	}
	resource, found, err := c.enrolledResource(ctx, tenant, environment, digest)
	if err != nil || !found || !c.Links.Serving(resource.Ref()) {
		return false, err
	}
	// Recheck authority after the relay; rotation or revocation never
	// inherits the serve peer of the former credential.
	if _, err = c.Store.AuthenticateEnvironmentExecutor(ctx, environment, digest); err != nil {
		return false, err
	}
	again, found, err := c.enrolledResource(ctx, tenant, environment, digest)
	return err == nil && found && again == resource && c.Links.Serving(resource.Ref()), err
}

// enrolledResource reads the Environment's live Link resource and reports
// whether it has one; the resource must be an enrollment served with the
// credential.
func (c *Connections) enrolledResource(ctx context.Context, tenant, environment, digest string) (sandboxbootstrap.Resource, bool, error) {
	authority, err := c.Store.GetEnvironmentResource(ctx, tenant, environment)
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
