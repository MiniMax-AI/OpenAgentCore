// Package runtime connects execution devices without product dependencies.
package runtime

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
)

// NewGateway serves the V1 daemon executor transport for both managed and
// user-managed Runtime. It authenticates devices with credentials, records
// their heartbeats with heartbeat, drains an archived Session's cancellation
// receipts through cancellations and gives agent hosts' binds their Link
// fields from links. Its credentials never grant public Session API access.
func NewGateway(credentials runtimegateway.RuntimeStore, heartbeat runtimegateway.HeartbeatTouch, cancellations runtimegateway.ArchivedCancellationStore, links *runtimegateway.LinkAuthority, publicWSURL string) (http.Handler, *runtimegateway.Registry, error) {
	if credentials == nil || heartbeat == nil || cancellations == nil || links == nil {
		return nil, nil, errors.New("daemon gateway dependencies are required")
	}
	registry := runtimegateway.NewRegistry()
	h := runtimegateway.NewHandler(runtimegateway.HandlerConfig{
		Authenticator: runtimegateway.NewAuthenticator(credentials), Registry: registry,
		Heartbeat: heartbeat, ArchivedCancellations: cancellations, Links: links, PublicWSURL: publicWSURL,
	})
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { runtimegateway.RegisterRoutes(r, h) })
	return r, registry, nil
}

// CloseConnections releases upgraded WebSockets, which http.Server.Shutdown
// does not close. Call after stopping new HTTP upgrades.
func CloseConnections(registry *runtimegateway.Registry) {
	for _, id := range registry.Devices() {
		if session, err := registry.LookupDevice(id); err == nil {
			session.Close("execution service shutting down")
		}
	}
}
