package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/adminaudit"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

type ProjectAPIKeyStore interface {
	CreateProjectAPIKey(context.Context, string, string) (store.IssuedProjectAPIKey, error)
	GetProjectAPIKey(context.Context, string) (store.ProjectAPIKeyBinding, error)
	ListProjectAPIKeys(context.Context, string, int, bool) (store.ProjectAPIKeyPage, error)
	ResetProjectAPIKey(context.Context, string, string) (store.IssuedProjectAPIKey, error)
	RevokeProjectAPIKey(context.Context, string) error
	ResolveProjectAPIKey(context.Context, string) (store.ProjectAPIKeyBinding, error)
}
type ProjectAPIKeyRequest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ProjectAPIKeyResetRequest struct {
	RequestID string `json:"request_id"`
}
type ProjectAPIKeyList = store.ProjectAPIKeyPage

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
	r.Route("/core/v1/admin/api-keys", func(r chi.Router) {
		r.Use(h.deploymentAuth.authenticate)
		r.Get("/", h.listProjectAPIKeys)
		r.Post("/", h.createProjectAPIKey)
		r.Get("/{key_id}", h.getProjectAPIKey)
		r.Delete("/{key_id}", h.revokeProjectAPIKey)
		r.Post("/{key_id}/reset", h.resetProjectAPIKey)
	})
}

// staticBinding is retained only for static read provenance selectors.
func (a *Authenticator) staticBinding(value string) (identity.Principal, bool) {
	digest, err := hex.DecodeString(value)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != value {
		return identity.Principal{}, false
	}
	p, ok := a.principals[[sha256.Size]byte(digest)]
	return p, ok
}
func (h *Handler) resolveAdminKey(ctx context.Context, id string) (store.ProjectAPIKeyBinding, error) {
	if strings.HasPrefix(id, "static:") {
		digest := strings.TrimPrefix(id, "static:")
		p, ok := h.auth.staticBinding(digest)
		if !ok {
			return store.ProjectAPIKeyBinding{}, store.ErrNotFound
		}
		raw, _ := hex.DecodeString(digest)
		source := h.auth.sources[[32]byte(raw)]
		return store.ProjectAPIKeyBinding{Principal: p, Key: store.ProjectAPIKey{ID: id, Name: source.Name, Prefix: source.Prefix, Kind: "static", TenantID: p.TenantID, OrganizationID: p.OrganizationID, ProjectID: p.ProjectID}}, nil
	}
	return h.projectKeys.GetProjectAPIKey(ctx, id)
}
func (h *Handler) adminKeyScope(w http.ResponseWriter, r *http.Request) (store.ProjectAPIKeyBinding, bool) {
	binding, err := h.resolveAdminKey(r.Context(), chi.URLParam(r, "key_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return store.ProjectAPIKeyBinding{}, false
	}
	setAdminAuditSource(r, binding.Key.ID)
	return binding, true
}
func setAdminAuditSource(r *http.Request, keyID string) {
	digest, _ := projectBearerDigest(r)
	requestID, _ := log.RequestIDFromContext(r.Context())
	source := adminaudit.Source{CredentialID: hex.EncodeToString(digest[:])[:8], ActorLabel: r.Header.Get("X-Core-Console-Actor"), RequestID: requestID, TraceID: requestID, TargetKeyID: keyID}
	if carrier, ok := log.TraceFromContext(r.Context()); ok {
		source.TraceID = carrier.Trace.String()
	}
	*r = *r.WithContext(adminaudit.WithSource(r.Context(), source))
}
func (h *Handler) listAdminKeyBindings(ctx context.Context, after string, limit int, ascending bool) (store.ProjectAPIKeyPage, error) {
	if limit < 1 || limit > 500 {
		return store.ProjectAPIKeyPage{}, store.ErrInvalidInput
	}
	if after != "" {
		if _, err := h.resolveAdminKey(ctx, after); err != nil {
			return store.ProjectAPIKeyPage{}, err
		}
	}
	page, err := h.projectKeys.ListProjectAPIKeys(ctx, after, limit, ascending)
	if err != nil {
		return page, err
	}
	for _, source := range h.auth.sources {
		if after == "" || (ascending && source.KeyID > after) || (!ascending && source.KeyID < after) {
			binding, err := h.resolveAdminKey(ctx, source.KeyID)
			if err != nil {
				return store.ProjectAPIKeyPage{}, err
			}
			page.Data = append(page.Data, binding.Key)
		}
	}
	sort.Slice(page.Data, func(i, j int) bool {
		if ascending {
			return page.Data[i].ID < page.Data[j].ID
		}
		return page.Data[i].ID > page.Data[j].ID
	})
	if len(page.Data) > limit {
		page.HasMore = true
		page.Data = page.Data[:limit]
	}
	if page.Data == nil {
		page.Data = []store.ProjectAPIKey{}
	}
	return page, nil
}

// @Summary List independent API key spaces
// @Tags Administrator API Keys
// @Produce json
// @Security DeploymentAdminAuth
// @Param after query string false "Key ID cursor"
// @Param limit query int false "Page size (1-500)"
// @Param order query string false "asc or desc by key ID"
// @Success 200 {object} store.ProjectAPIKeyPage
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /core/v1/admin/api-keys [get]
func (h *Handler) listProjectAPIKeys(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	limit := 20
	ascending := false
	for _, name := range []string{"after", "limit", "order"} {
		if len(values[name]) > 1 {
			writeStoreError(w, r, store.ErrInvalidInput)
			return
		}
	}
	if raw, ok := values["limit"]; ok {
		n, err := strconv.Atoi(raw[0])
		if err != nil {
			writeStoreError(w, r, store.ErrInvalidInput)
			return
		}
		limit = n
	}
	if raw, ok := values["order"]; ok {
		if raw[0] != "asc" && raw[0] != "desc" {
			writeStoreError(w, r, store.ErrInvalidInput)
			return
		}
		ascending = raw[0] == "asc"
	}
	page, err := h.listAdminKeyBindings(r.Context(), values.Get("after"), limit, ascending)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// @Summary Issue an API key with a new independent space
// @Tags Administrator API Keys
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param body body api.ProjectAPIKeyRequest true "Stable key ID and display name"
// @Success 201 {object} store.IssuedProjectAPIKey
// @Failure 400,401,409,500 {object} v1.ErrorResponse
// @Router /core/v1/admin/api-keys [post]
func (h *Handler) createProjectAPIKey(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBodyLimit(w, r, 4096, "API key request is too large.")
	if !ok {
		return
	}
	var input ProjectAPIKeyRequest
	if decodeInputObject(raw, &input, "id", "name") != nil || !executorManagementID(input.ID) {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	setAdminAuditSource(r, input.ID)
	key, err := h.projectKeys.CreateProjectAPIKey(r.Context(), input.ID, input.Name)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, key)
}

// @Summary Get safe API key metadata including revoked keys
// @Tags Administrator API Keys
// @Produce json
// @Security DeploymentAdminAuth
// @Param key_id path string true "API key ID"
// @Success 200 {object} store.ProjectAPIKey
// @Failure 401,404,500 {object} v1.ErrorResponse
// @Router /core/v1/admin/api-keys/{key_id} [get]
func (h *Handler) getProjectAPIKey(w http.ResponseWriter, r *http.Request) {
	binding, ok := h.adminKeyScope(w, r)
	if ok {
		writeJSON(w, http.StatusOK, binding.Key)
	}
}

// @Summary Reset a key secret while preserving its space and identity
// @Tags Administrator API Keys
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param key_id path string true "API key UUID"
// @Param body body api.ProjectAPIKeyResetRequest true "Unique reset request ID; duplicates return conflict"
// @Success 200 {object} store.IssuedProjectAPIKey
// @Failure 400,401,404,409,500 {object} v1.ErrorResponse
// @Router /core/v1/admin/api-keys/{key_id}/reset [post]
func (h *Handler) resetProjectAPIKey(w http.ResponseWriter, r *http.Request) {
	binding, ok := h.adminKeyScope(w, r)
	if !ok {
		return
	}
	if binding.Key.Kind == "static" {
		writeError(w, 409, "static_api_key", "Static keys are managed through deployment configuration.")
		return
	}
	raw, ok := readJSONBodyLimit(w, r, 4096, "API key request is too large.")
	if !ok {
		return
	}
	var input ProjectAPIKeyResetRequest
	if decodeInputObject(raw, &input, "request_id") != nil || !executorManagementID(input.RequestID) {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	key, err := h.projectKeys.ResetProjectAPIKey(r.Context(), binding.Key.ID, input.RequestID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, key)
}

// @Summary Revoke authentication while retaining the key space and assets
// @Tags Administrator API Keys
// @Produce json
// @Security DeploymentAdminAuth
// @Param key_id path string true "API key UUID"
// @Success 200 {object} api.SandboxMutationResponse
// @Failure 401,404,409,500 {object} v1.ErrorResponse
// @Router /core/v1/admin/api-keys/{key_id} [delete]
func (h *Handler) revokeProjectAPIKey(w http.ResponseWriter, r *http.Request) {
	binding, ok := h.adminKeyScope(w, r)
	if !ok {
		return
	}
	if binding.Key.Kind == "static" {
		writeError(w, 409, "static_api_key", "Static keys are managed through deployment configuration.")
		return
	}
	if err := h.projectKeys.RevokeProjectAPIKey(r.Context(), binding.Key.ID); err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, SandboxMutationResponse{ID: binding.Key.ID, Deleted: true})
}

func (h *Handler) adminAuditContext(r *http.Request, keyID string) context.Context {
	setAdminAuditSource(r, keyID)
	return r.Context()
}
