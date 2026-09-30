package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// EnvironmentExecutorStore manages the executor credentials of a Project's
// self_hosted Environments. The Project's principal is the credential's
// execution principal; the Core key that authorizes the request is not.
type EnvironmentExecutorStore interface {
	ProjectExecutorCredentialState(context.Context, identity.Principal, string) (store.ExecutorCredentialState, error)
	IssueProjectExecutorCredential(context.Context, identity.Principal, string, string, bool) (store.IssuedExecutorCredential, error)
	RevokeProjectExecutorCredential(context.Context, identity.Principal, string, string) error
}

type EnvironmentExecutorCredentialRequest struct {
	KeyID  string `json:"key_id" format:"uuid"`
	Rotate bool   `json:"rotate,omitempty"`
}

// ExecutorCredentialList holds credential metadata only, never a secret.
type ExecutorCredentialList struct {
	Data       []store.ExecutorCredential `json:"data" binding:"required"`
	Connection ExecutorConnection         `json:"connection" binding:"required"`
}

// ExecutorConnection reports binding history and current Core-observed connectivity.
// Heartbeat times are observations, not execution or native readiness.
type ExecutorConnection struct {
	Status     string     `json:"status" binding:"required" enums:"never_enrolled,connected,disconnected"`
	BoundKeyID *string    `json:"bound_key_id" binding:"required" extensions:"x-nullable" format:"uuid"`
	EnrolledAt *time.Time `json:"enrolled_at" binding:"required" format:"date-time" extensions:"x-nullable"`
	LastSeenAt *time.Time `json:"last_seen_at" binding:"required" format:"date-time" extensions:"x-nullable"`
}

// WithExecutorConnections observes current authority and its actual gateway peer.
// The observer runs after the store snapshot closes and must recheck authority
// after inspecting the peer. Without an observer, no binding is called connected.
func WithExecutorConnections(observe func(context.Context, string, string) (bool, error)) Option {
	return func(h *Handler) { h.executorConnections = observe }
}

// registerExecutorCredentialRoutes adds executor credential issuance to the
// Core-key-authenticated /core/v1 router.
func (h *Handler) registerExecutorCredentialRoutes(r chi.Router) {
	s, ok := h.store.(EnvironmentExecutorStore)
	if !ok || h.projectKeys == nil {
		return
	}
	const path = "/projects/{project_id}/environments/{environment_id}/executor-credentials"
	r.Get("/projects/{project_id}/environments/{environment_id}/installation", h.getEnvironmentInstallation)
	r.Get(path, func(w http.ResponseWriter, r *http.Request) { h.listExecutorCredentials(w, r, s) })
	r.Post(path, func(w http.ResponseWriter, r *http.Request) { h.issueExecutorCredential(w, r, s) })
	r.Delete(path+"/{key_id}", func(w http.ResponseWriter, r *http.Request) { h.revokeExecutorCredential(w, r, s) })
}

// @Summary List a self_hosted Environment's executor credentials
// @Description Core key only. Returns metadata of the credentials restricted to this Environment, oldest first; secrets are never listed. Connection combines current credential authority and an open matching gateway peer; timestamps are historical observations, not readiness. Without a gateway it is never connected. The Environment must be a self_hosted Environment of the Project whose Session exists; otherwise 404.
// @Tags Executor Credentials
// @Produce json
// @Security DeploymentAdminAuth
// @Param project_id path string true "Project UUID"
// @Param environment_id path string true "Environment UUID"
// @Success 200 {object} api.ExecutorCredentialList
// @Failure 401,404,500 {object} CoreErrorResponse
// @Router /core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials [get]
func (h *Handler) listExecutorCredentials(w http.ResponseWriter, r *http.Request, s EnvironmentExecutorStore) {
	binding, ok := h.adminProjectScope(w, r)
	if !ok {
		return
	}
	state, err := s.ProjectExecutorCredentialState(r.Context(), binding.Principal, chi.URLParam(r, "environment_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	connection := ExecutorConnection{Status: "never_enrolled"}
	observed := state.Connection
	if observed.DeviceID != "" {
		connection = ExecutorConnection{Status: "disconnected", BoundKeyID: observed.BoundKeyID, EnrolledAt: observed.EnrolledAt, LastSeenAt: observed.LastSeenAt}
		if observed.EnvironmentStatus == "connected" && observed.CredentialHash != "" && h.executorConnections != nil {
			connected, err := h.executorConnections(r.Context(), state.EnvironmentID, observed.CredentialHash)
			if err != nil && !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrDeviceBindingConflict) {
				writeStoreError(w, r, err)
				return
			}
			if err == nil && connected {
				connection.Status = "connected"
			}
		}
	}
	writeJSON(w, http.StatusOK, ExecutorCredentialList{Data: state.Credentials, Connection: connection})
}

// @Summary Issue or explicitly rotate a self_hosted Environment executor credential
// @Description Core key only. Returns a connect-only secret once, restricted to daemon enrollment and connection for this Environment, with the Project's principal as its execution principal. Repeating an issuance key_id returns 409 executor_credential_exists; after an uncertain response, list the credentials and rotate that key_id explicitly. Rotation keeps the key's Environment, invalidates the old secret and restores a revoked key; rotating an unknown key_id returns 404. In an archived Project, issuance and rotation return 409 project_archived. The Environment must be a self_hosted Environment of the Project whose Session exists; otherwise 404. Each write records an administrator audit entry without the secret.
// @Tags Executor Credentials
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param project_id path string true "Project UUID"
// @Param environment_id path string true "Environment UUID"
// @Param body body api.EnvironmentExecutorCredentialRequest true "Request"
// @Success 201 {object} store.IssuedExecutorCredential
// @Failure 400,401,404,409,500 {object} CoreErrorResponse
// @Router /core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials [post]
func (h *Handler) issueExecutorCredential(w http.ResponseWriter, r *http.Request, s EnvironmentExecutorStore) {
	// Check order: request body (400), target (404), archived Project (409),
	// then the key itself (409 exists, or 404 for rotating an unknown key).
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var input EnvironmentExecutorCredentialRequest
	var fields map[string]json.RawMessage
	if decodeInputObject(raw, &input, "key_id", "rotate") != nil || json.Unmarshal(raw, &fields) != nil || bytes.Equal(bytes.TrimSpace(fields["rotate"]), []byte("null")) || !executorManagementID(input.KeyID) {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	binding, ok := h.adminProjectScope(w, r)
	if !ok {
		return
	}
	credential, err := s.IssueProjectExecutorCredential(r.Context(), binding.Principal, chi.URLParam(r, "environment_id"), input.KeyID, input.Rotate)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, credential)
}

// @Summary Revoke a self_hosted Environment executor credential
// @Description Core key only. Revokes one credential restricted to this Environment; repeated revocation is safe and it also works in an archived Project. Revocation denies future enrollment and connection but does not stop executor-owned compute. The Environment must be a self_hosted Environment of the Project whose Session exists; otherwise 404.
// @Tags Executor Credentials
// @Security DeploymentAdminAuth
// @Param project_id path string true "Project UUID"
// @Param environment_id path string true "Environment UUID"
// @Param key_id path string true "Executor key UUID"
// @Success 204
// @Failure 401,404,500 {object} CoreErrorResponse
// @Router /core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials/{key_id} [delete]
func (h *Handler) revokeExecutorCredential(w http.ResponseWriter, r *http.Request, s EnvironmentExecutorStore) {
	binding, ok := h.adminProjectScope(w, r)
	if !ok {
		return
	}
	keyID := chi.URLParam(r, "key_id")
	if !executorManagementID(keyID) {
		writeStoreError(w, r, store.ErrNotFound)
		return
	}
	if err := s.RevokeProjectExecutorCredential(r.Context(), binding.Principal, chi.URLParam(r, "environment_id"), keyID); err != nil {
		writeStoreError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func executorManagementID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}
