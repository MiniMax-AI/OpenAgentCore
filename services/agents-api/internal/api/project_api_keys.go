package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

type ProjectAPIKeyStore interface {
	CreateProjectAPIKey(context.Context, string, identity.Principal, string, string) (store.IssuedProjectAPIKey, error)
	ListProjectAPIKeys(context.Context, string, identity.Principal) ([]store.ProjectAPIKey, error)
	RevokeProjectAPIKey(context.Context, string, identity.Principal, string) error
	ResolveProjectAPIKey(context.Context, string) (store.ProjectAPIKeyBinding, error)
}

type ProjectAPIKeyRequest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ProjectAPIKeyList struct {
	Data    []store.ProjectAPIKey `json:"data"`
	HasMore bool                  `json:"has_more"`
}

func WithProjectAPIKeys(s ProjectAPIKeyStore, auth *DeploymentAuthenticator) Option {
	return func(h *Handler) {
		h.projectKeys = s
		if auth != nil {
			h.deploymentAuth = auth
		}
	}
}

func (h *Handler) registerProjectAPIKeyRoutes(r chi.Router) {
	if h.projectKeys == nil || h.deploymentAuth == nil {
		return
	}
	r.Route("/core/v1/project-api-keys/{binding_digest}", func(r chi.Router) {
		r.Use(h.deploymentAuth.authenticate)
		r.Get("/", h.listProjectAPIKeys)
		r.Post("/", h.createProjectAPIKey)
		r.Delete("/{key_id}", h.revokeProjectAPIKey)
	})
}

// staticBinding resolves a non-secret selector, never a credential or caller
// supplied principal. Derived keys cannot themselves become parent bindings.
func (a *Authenticator) staticBinding(value string) (identity.Principal, bool) {
	digest, err := hex.DecodeString(value)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != value {
		return identity.Principal{}, false
	}
	principal, ok := a.principals[[sha256.Size]byte(digest)]
	return principal, ok
}

func (h *Handler) projectAPIKeyBinding(w http.ResponseWriter, r *http.Request) (string, identity.Principal, bool) {
	digest := chi.URLParam(r, "binding_digest")
	principal, ok := h.auth.staticBinding(digest)
	if !ok {
		writeStoreError(w, r, store.ErrNotFound)
		return "", identity.Principal{}, false
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeStoreError(w, r, store.ErrInvalidInput)
		return "", identity.Principal{}, false
	}
	return digest, principal, true
}

// @Summary List project API keys for a configured caller binding
// @Description Core extension requiring deployment administrator authority. The binding digest selects only a statically configured caller; it does not authenticate. Returns safe metadata, never key secrets or token digests. Derived keys freeze that caller's principal and stop authenticating when the static parent is removed or rebound.
// @Tags Project API Keys
// @Produce json
// @Security DeploymentAdminAuth
// @Param binding_digest path string true "Configured caller SHA-256 binding selector"
// @Success 200 {object} api.ProjectAPIKeyList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /core/v1/project-api-keys/{binding_digest} [get]
func (h *Handler) listProjectAPIKeys(w http.ResponseWriter, r *http.Request) {
	digest, principal, ok := h.projectAPIKeyBinding(w, r)
	if !ok {
		return
	}
	keys, err := h.projectKeys.ListProjectAPIKeys(r.Context(), digest, principal)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if keys == nil {
		keys = []store.ProjectAPIKey{}
	}
	writeJSON(w, http.StatusOK, ProjectAPIKeyList{Data: keys})
}

// @Summary Create a project API key for a configured caller binding
// @Description Core extension requiring deployment administrator authority. Generates an independent random secret, stores only its digest and returns the secret once. A duplicate request ID returns 409 without replaying the secret. After an uncertain response, list the ID and revoke it explicitly before creating another key. The key inherits the frozen static caller principal and cannot manage keys or deployment resources.
// @Tags Project API Keys
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param binding_digest path string true "Configured caller SHA-256 binding selector"
// @Param body body api.ProjectAPIKeyRequest true "Key ID and display name"
// @Success 201 {object} store.IssuedProjectAPIKey
// @Failure 400,401,404,409,500 {object} v1.ErrorResponse
// @Router /core/v1/project-api-keys/{binding_digest} [post]
func (h *Handler) createProjectAPIKey(w http.ResponseWriter, r *http.Request) {
	digest, principal, ok := h.projectAPIKeyBinding(w, r)
	if !ok {
		return
	}
	raw, ok := readJSONBodyLimit(w, r, 4096, "API key request is too large.")
	if !ok {
		return
	}
	var input ProjectAPIKeyRequest
	if decodeInputObject(raw, &input, "id", "name") != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	containsControl := strings.IndexFunc(input.Name, unicode.IsControl) >= 0
	input.Name = strings.TrimSpace(input.Name)
	if !executorManagementID(input.ID) || input.Name == "" || utf8.RuneCountInString(input.Name) > 80 || containsControl {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	key, err := h.projectKeys.CreateProjectAPIKey(r.Context(), digest, principal, input.ID, input.Name)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, key)
}

// @Summary Revoke a project API key
// @Description Core extension requiring deployment administrator authority. Scopes the key ID to the exact configured parent binding and principal. Revocation prevents future authenticated requests; it does not cancel already admitted operations or streams. Missing and foreign IDs return the same 404 response.
// @Tags Project API Keys
// @Produce json
// @Security DeploymentAdminAuth
// @Param binding_digest path string true "Configured caller SHA-256 binding selector"
// @Param key_id path string true "Project API key UUID"
// @Success 200 {object} api.SandboxMutationResponse
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /core/v1/project-api-keys/{binding_digest}/{key_id} [delete]
func (h *Handler) revokeProjectAPIKey(w http.ResponseWriter, r *http.Request) {
	digest, principal, ok := h.projectAPIKeyBinding(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "key_id")
	if !executorManagementID(id) {
		writeStoreError(w, r, store.ErrNotFound)
		return
	}
	if err := h.projectKeys.RevokeProjectAPIKey(r.Context(), digest, principal, id); err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, SandboxMutationResponse{ID: id, Deleted: true})
}
