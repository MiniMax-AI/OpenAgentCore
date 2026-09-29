package api

import (
	"context"
	"net/http"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/nativeinstaller"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

type environmentInstallationStore interface {
	AuthorizeEnvironmentInstallation(context.Context, identity.Principal, string, string) (string, int64, error)
	ValidateEnvironmentInstallation(context.Context, string, string) (store.InstallationAuthorization, error)
	ClaimEnvironmentInstallation(context.Context, string, string, string) error
}

func WithNativeInstaller(catalog *nativeinstaller.Catalog, version string) Option {
	return func(h *Handler) { h.nativeInstaller, h.nativeVersion = catalog, version }
}

func (h *Handler) installationFor(ctx context.Context, principal identity.Principal, environment string) (*v1.EnvironmentInstallation, error) {
	result := &v1.EnvironmentInstallation{Status: "unavailable", Version: h.nativeVersion, Message: "This Core has no matching native installation distribution. Ask its operator to install the qualified release artifacts."}
	s, ok := h.store.(environmentInstallationStore)
	if !ok || h.nativeInstaller == nil {
		return result, nil
	}
	token, expires, err := s.AuthorizeEnvironmentInstallation(ctx, principal, environment, h.nativeVersion)
	if err != nil {
		return nil, err
	}
	origin := strings.TrimSuffix(h.executorURL, "/api/v1/agent-daemon/ws")
	origin = strings.Replace(strings.Replace(origin, "wss://", "https://", 1), "ws://", "http://", 1)
	return &v1.EnvironmentInstallation{Status: "available", Version: h.nativeVersion, ExpiresAt: expires, Commands: h.nativeInstaller.Commands(origin, token)}, nil
}

func (h *Handler) addSessionInstallation(w http.ResponseWriter, r *http.Request, response *v1.Session) error {
	if response.Environment.Type != "self_hosted" || h.nativeVersion == "" {
		return nil
	}
	principal, ok := r.Context().Value(principalContextKey{}).(identity.Principal)
	if !ok {
		return nil
	}
	installation, err := h.installationFor(r.Context(), principal, response.Environment.ID)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	response.XAgentsCore = &v1.SessionCore{Installation: installation}
	return nil
}

func (h *Handler) registerNativeInstallationRoutes(r chi.Router) {
	if h.nativeVersion == "" {
		return
	}
	if h.nativeInstaller != nil {
		r.Handle("/api/v1/agent-daemon/install/*", h.nativeInstaller)
	}
	r.Post("/api/v1/agent-daemon/installation", h.prepareNativeInstallation)
	r.Post("/api/v1/agent-daemon/installation/claim", h.claimNativeInstallation)
}

func (h *Handler) installationAuthorization(w http.ResponseWriter, r *http.Request) (environmentInstallationStore, store.InstallationAuthorization, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	s, ok := h.store.(environmentInstallationStore)
	if !ok || h.nativeInstaller == nil {
		writeError(w, 503, "installation_unavailable", "Matching native installation artifacts are unavailable.")
		return nil, store.InstallationAuthorization{}, "", false
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(r.Header.Values("Authorization")) != 1 || len(parts) != 2 || parts[0] != "Bearer" {
		writeStoreError(w, r, store.ErrInstallationAuthorization)
		return nil, store.InstallationAuthorization{}, "", false
	}
	claim, err := s.ValidateEnvironmentInstallation(r.Context(), parts[1], h.nativeVersion)
	if err != nil {
		writeStoreError(w, r, err)
		return nil, claim, "", false
	}
	return s, claim, parts[1], true
}

// @Summary Resolve a native installation authorization
// @Description Accepts a short-lived Environment installation Bearer authorization, not a Project or Core key. Returns frozen connection constraints; it does not claim or rotate credentials.
// @Tags Native Installation
// @Produce json
// @Success 200 {object} v1.NativeInstallationContext
// @Failure 401,404,503 {object} CoreErrorResponse
// @Router /api/v1/agent-daemon/installation [post]
func (h *Handler) prepareNativeInstallation(w http.ResponseWriter, r *http.Request) {
	_, claim, _, ok := h.installationAuthorization(w, r)
	if !ok {
		return
	}
	environment, err := h.store.GetEnvironment(r.Context(), claim.Principal.TenantID, claim.Environment)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	session, err := h.store.GetSession(r.Context(), claim.Principal.TenantID, environment.SessionID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response, err := sessionResponse(session, h.executorURL)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.NativeInstallationContext{Version: h.nativeVersion, ProtocolVersion: proto.Version, EnvironmentID: claim.Environment, RemoteURL: h.executorURL, Workspace: response.Environment.WorkspaceDirectory, Harness: session.Engine})
}

type NativeInstallationClaim struct {
	ExecutorToken string `json:"executor_token"`
}

// @Summary Claim an Environment's installation credential
// @Description A valid installation Bearer authorization can claim one connect-only key. The client persists its generated secret before submitting it. Retries must present that same secret; a different, rotated or revoked credential is never replaced.
// @Tags Native Installation
// @Accept json
// @Param body body api.NativeInstallationClaim true "Locally persisted executor secret"
// @Success 204
// @Failure 400,401,409,503 {object} CoreErrorResponse
// @Router /api/v1/agent-daemon/installation/claim [post]
func (h *Handler) claimNativeInstallation(w http.ResponseWriter, r *http.Request) {
	s, _, token, ok := h.installationAuthorization(w, r)
	if !ok {
		return
	}
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var input NativeInstallationClaim
	if decodeInputObject(raw, &input, "executor_token") != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	if err := s.ClaimEnvironmentInstallation(r.Context(), token, h.nativeVersion, input.ExecutorToken); err != nil {
		writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// @Summary Get a self_hosted Session's installation commands
// @Description Core key only. The commands contain a 30-minute installation authorization, never an executor secret. Web displays these same commands provided in public Session creation and detail responses.
// @Tags Native Installation
// @Security DeploymentAdminAuth
// @Param project_id path string true "Project UUID"
// @Param environment_id path string true "Environment UUID"
// @Success 200 {object} v1.EnvironmentInstallation
// @Failure 401,404,409 {object} CoreErrorResponse
// @Router /core/v1/projects/{project_id}/environments/{environment_id}/installation [get]
func (h *Handler) getEnvironmentInstallation(w http.ResponseWriter, r *http.Request) {
	binding, ok := h.adminProjectScope(w, r)
	if !ok {
		return
	}
	environment := chi.URLParam(r, "environment_id")
	s, ok := h.store.(EnvironmentExecutorStore)
	if !ok {
		writeStoreError(w, r, store.ErrNotFound)
		return
	}
	if _, err := s.ProjectExecutorCredentialState(r.Context(), binding.Principal, environment); err != nil {
		writeStoreError(w, r, err)
		return
	}
	result, err := h.installationFor(r.Context(), binding.Principal, environment)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}
