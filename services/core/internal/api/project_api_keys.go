package api

import (
	"context"
	"encoding/hex"
	"net/http"
	"strconv"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Projects manages Projects and their API keys, and resolves a Project API key
// digest to its current binding for authentication.
type Projects interface {
	CreateProject(context.Context, string, string) (store.Project, error)
	GetProject(context.Context, string) (store.ProjectBinding, error)
	ListProjects(context.Context, string, int, bool) (store.ProjectPage, error)
	RenameProject(context.Context, string, string) (store.Project, error)
	ArchiveProject(context.Context, string) (store.Project, error)
	CreateProjectAPIKey(context.Context, string, string, string) (store.IssuedProjectAPIKey, error)
	ListProjectAPIKeys(context.Context, string, string, int, bool) (store.ProjectAPIKeyPage, error)
	RevokeProjectAPIKey(context.Context, string, string) error
	ResolveProjectAPIKey(context.Context, string) (store.ProjectAPIKeyBinding, error)
}
type ProjectRequest struct {
	Name string `json:"name"`
}
type ProjectAPIKeyRequest struct {
	Name string `json:"name"`
}

func (h *Handler) registerProjectAPIKeyRoutes(r chi.Router) {
	r.Get("/projects", h.listProjects)
	r.Post("/projects", h.createProject)
	r.Post("/projects/{project_id}", h.renameProject)
	r.Post("/projects/{project_id}/archive", h.archiveProject)
	r.Get("/projects/{project_id}/keys", h.listProjectAPIKeys)
	r.Post("/projects/{project_id}/keys", h.createProjectAPIKey)
	r.Delete("/projects/{project_id}/keys/{key_id}", h.revokeProjectAPIKey)
}
func (h *Handler) adminProjectScope(w http.ResponseWriter, r *http.Request) (store.ProjectBinding, bool) {
	p, err := h.Projects.GetProject(r.Context(), chi.URLParam(r, "project_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return store.ProjectBinding{}, false
	}
	setAdminAuditSource(r, p.Project.ID)
	return p, true
}
func setAdminAuditSource(r *http.Request, projectID string) {
	digest, _ := projectBearerDigest(r)
	requestID, _ := log.RequestIDFromContext(r.Context())
	source := adminaudit.Source{CredentialID: hex.EncodeToString(digest[:])[:8], ActorLabel: r.Header.Get("X-Core-Console-Actor"), RequestID: requestID, TraceID: requestID, ProjectID: projectID}
	if carrier, ok := log.TraceFromContext(r.Context()); ok {
		source.TraceID = carrier.Trace.String()
	}
	*r = *r.WithContext(adminaudit.WithSource(r.Context(), source))
}
func adminCatalogPage(r *http.Request) (string, int, bool, error) {
	values := r.URL.Query()
	limit := 20
	ascending := false
	for _, name := range []string{"after", "limit", "order"} {
		if len(values[name]) > 1 {
			return "", 0, false, store.ErrInvalidInput
		}
	}
	if raw, ok := values["limit"]; ok {
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 1 || n > 100 {
			return "", 0, false, store.ErrInvalidInput
		}
		limit = n
	}
	if raw, ok := values["order"]; ok {
		if raw[0] != "asc" && raw[0] != "desc" {
			return "", 0, false, store.ErrInvalidInput
		}
		ascending = raw[0] == "asc"
	}
	return values.Get("after"), limit, ascending, nil
}

// @Summary List Projects and active key counts
// @Tags Administrator Projects
// @Produce json
// @Security DeploymentAdminAuth
// @Param after query string false "Project ID cursor"
// @Param limit query int false "Page size (1-100)"
// @Param order query string false "asc or desc by Project ID"
// @Success 200 {object} store.ProjectPage
// @Failure 400,401,404,500 {object} CoreErrorResponse
// @Router /core/v1/projects [get]
func (h *Handler) listProjects(w http.ResponseWriter, r *http.Request) {
	after, limit, ascending, err := adminCatalogPage(r)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	page, err := h.Projects.ListProjects(r.Context(), after, limit, ascending)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, 200, page)
}

// @Summary Create an empty independent Project
// @Tags Administrator Projects
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param body body api.ProjectRequest true "Project display name"
// @Success 201 {object} store.Project
// @Failure 400,401,409,500 {object} CoreErrorResponse
// @Router /core/v1/projects [post]
func (h *Handler) createProject(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBodyLimit(w, r, 4096, "Project request is too large.")
	if !ok {
		return
	}
	var input ProjectRequest
	if decodeInputObject(raw, &input, "name") != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	id := uuid.NewString()
	setAdminAuditSource(r, id)
	p, err := h.Projects.CreateProject(r.Context(), id, input.Name)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, 201, p)
}

// @Summary Rename a Project
// @Tags Administrator Projects
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param project_id path string true "Project UUID"
// @Param body body api.ProjectRequest true "Project display name"
// @Success 200 {object} store.Project
// @Failure 400,401,404,409,500 {object} CoreErrorResponse
// @Router /core/v1/projects/{project_id} [post]
func (h *Handler) renameProject(w http.ResponseWriter, r *http.Request) {
	binding, ok := h.adminProjectScope(w, r)
	if !ok {
		return
	}
	raw, ok := readJSONBodyLimit(w, r, 4096, "Project request is too large.")
	if !ok {
		return
	}
	var input ProjectRequest
	if decodeInputObject(raw, &input, "name") != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	p, err := h.Projects.RenameProject(r.Context(), binding.Project.ID, input.Name)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}

// @Summary Archive a Project and revoke all its keys while retaining assets
// @Tags Administrator Projects
// @Produce json
// @Security DeploymentAdminAuth
// @Param project_id path string true "Project UUID"
// @Success 200 {object} store.Project
// @Failure 401,404,409,500 {object} CoreErrorResponse
// @Router /core/v1/projects/{project_id}/archive [post]
func (h *Handler) archiveProject(w http.ResponseWriter, r *http.Request) {
	binding, ok := h.adminProjectScope(w, r)
	if !ok {
		return
	}
	p, err := h.Projects.ArchiveProject(r.Context(), binding.Project.ID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}

// @Summary List safe key metadata for a Project
// @Tags Administrator Projects
// @Produce json
// @Security DeploymentAdminAuth
// @Param project_id path string true "Project UUID"
// @Param after query string false "Key ID cursor"
// @Param limit query int false "Page size (1-100)"
// @Param order query string false "asc or desc by key ID"
// @Success 200 {object} store.ProjectAPIKeyPage
// @Failure 400,401,404,500 {object} CoreErrorResponse
// @Router /core/v1/projects/{project_id}/keys [get]
func (h *Handler) listProjectAPIKeys(w http.ResponseWriter, r *http.Request) {
	binding, ok := h.adminProjectScope(w, r)
	if !ok {
		return
	}
	after, limit, ascending, err := adminCatalogPage(r)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	page, err := h.Projects.ListProjectAPIKeys(r.Context(), binding.Project.ID, after, limit, ascending)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, 200, page)
}

// @Summary Issue an independent secret in an existing Project
// @Tags Administrator Projects
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param project_id path string true "Project UUID"
// @Param body body api.ProjectAPIKeyRequest true "Key display name"
// @Success 201 {object} store.IssuedProjectAPIKey
// @Failure 400,401,404,409,500 {object} CoreErrorResponse
// @Router /core/v1/projects/{project_id}/keys [post]
func (h *Handler) createProjectAPIKey(w http.ResponseWriter, r *http.Request) {
	binding, ok := h.adminProjectScope(w, r)
	if !ok {
		return
	}
	raw, ok := readJSONBodyLimit(w, r, 4096, "API key request is too large.")
	if !ok {
		return
	}
	var input ProjectAPIKeyRequest
	if decodeInputObject(raw, &input, "name") != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	key, err := h.Projects.CreateProjectAPIKey(r.Context(), binding.Project.ID, uuid.NewString(), input.Name)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, 201, key)
}

// @Summary Revoke one Project key while retaining shared assets
// @Tags Administrator Projects
// @Produce json
// @Security DeploymentAdminAuth
// @Param project_id path string true "Project UUID"
// @Param key_id path string true "API key UUID"
// @Success 200 {object} api.SandboxMutationResponse
// @Failure 401,404,409,500 {object} CoreErrorResponse
// @Router /core/v1/projects/{project_id}/keys/{key_id} [delete]
func (h *Handler) revokeProjectAPIKey(w http.ResponseWriter, r *http.Request) {
	binding, ok := h.adminProjectScope(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "key_id")
	if err := h.Projects.RevokeProjectAPIKey(r.Context(), binding.Project.ID, id); err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, 200, SandboxMutationResponse{ID: id, Deleted: true})
}
