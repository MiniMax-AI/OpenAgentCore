package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspaces"
)

// WorkspaceStorage reads and changes the independent filesystem selection and
// tenant capacity policy. Configure serializes selection changes and records
// the admin audit source.
type WorkspaceStorage interface {
	Selection(context.Context) (workspaces.Selection, error)
	Configure(context.Context, workspaces.SelectionInput) (workspaces.Selection, error)
}

// WorkspaceStorageInput selects an immutable configuration and the tenant
// capacity policy together. Credential is write-only and adapter-owned.
type WorkspaceStorageInput struct {
	ID                string          `json:"id" format:"uuid" binding:"required"`
	Adapter           string          `json:"adapter" binding:"required"`
	Parameters        json.RawMessage `json:"parameters" swaggertype:"object" binding:"required"`
	Credential        json.RawMessage `json:"credential,omitempty" swaggertype:"object"`
	TenantCapacityMiB *uint32         `json:"tenant_capacity_mib" binding:"required"`
}

// @Summary Retrieve workspace storage configuration
// @Description Core key only. Returns the immutable selected filesystem configuration and the tenant capacity policy, never a credential. No configuration returns 404.
// @Tags Workspace Storage
// @Produce json
// @Security DeploymentAdminAuth
// @Success 200 {object} workspaces.Selection
// @Failure 400,401,404,409,500,503 {object} CoreErrorResponse
// @Router /core/v1/workspace-storage [get]
func (h *Handler) getWorkspaceStorage(w http.ResponseWriter, r *http.Request) {
	selection, err := h.WorkspaceStorage.Selection(r.Context())
	if err != nil {
		writeWorkspaceStorageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, selection)
}

// @Summary Select workspace storage configuration
// @Description Core key only. Selects an immutable filesystem configuration and the tenant capacity policy in one change. Reusing an ID requires the same adapter, parameters and credential; an omitted credential retains the stored one. A positive tenant_capacity_mib requires, and is required by, a configuration that enforces capacity quotas. Conflicting ownership returns 409.
// @Tags Workspace Storage
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param body body api.WorkspaceStorageInput true "Workspace storage selection"
// @Success 200 {object} workspaces.Selection
// @Failure 400,401,404,409,500,503 {object} CoreErrorResponse
// @Router /core/v1/workspace-storage [put]
func (h *Handler) putWorkspaceStorage(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBodyLimit(w, r, 16384, "Workspace storage configuration is too large.")
	if !ok {
		return
	}
	var input WorkspaceStorageInput
	if decodeInputObject(raw, &input, "id", "adapter", "parameters", "credential", "tenant_capacity_mib") != nil || input.TenantCapacityMiB == nil {
		writeWorkspaceStorageError(w, r, workspacefs.ErrInvalid)
		return
	}
	selection := workspaces.SelectionInput{Selection: workspaces.Selection{
		Configuration:     workspacefs.Configuration{ID: input.ID, Adapter: input.Adapter, Parameters: input.Parameters},
		TenantCapacityMiB: *input.TenantCapacityMiB,
	}, Credential: input.Credential}
	if selection.Configuration.Validate() != nil {
		writeWorkspaceStorageError(w, r, workspacefs.ErrInvalid)
		return
	}
	setAdminAuditSource(r, "")
	result, err := h.WorkspaceStorage.Configure(r.Context(), selection)
	if err != nil {
		writeWorkspaceStorageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeWorkspaceStorageError(w http.ResponseWriter, r *http.Request, err error) {
	if writeWorkspaceError(w, err) || writeAuditSourceError(w, r, err) {
		return
	}
	writeInternalError(w, r)
}

// writeWorkspaceError is shared by operator configuration and public admission.
// Native adapter errors may contain paths; expose only fixed protocol messages.
func writeWorkspaceError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, workspacefs.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_workspace_configuration", "Invalid workspace storage configuration.")
	case errors.Is(err, workspacefs.ErrUnsupported):
		writeError(w, http.StatusBadRequest, "workspace_operation_unsupported", "The selected workspace storage does not support this operation or sandbox combination.")
	case errors.Is(err, workspacefs.ErrOwnership), errors.Is(err, workspaces.ErrConflict):
		writeError(w, http.StatusConflict, "workspace_storage_conflict", "Workspace storage ownership or configuration conflicts with the current state.")
	case errors.Is(err, workspacefs.ErrNotFound), errors.Is(err, workspaces.ErrNotFound), errors.Is(err, workspaces.ErrNotConfigured):
		writeError(w, http.StatusNotFound, "workspace_storage_not_found", "Workspace storage is not configured or the requested object does not exist.")
	case errors.Is(err, workspacefs.ErrUnavailable), errors.Is(err, workspacefs.ErrUnconfirmed):
		writeError(w, http.StatusServiceUnavailable, "workspace_storage_unavailable", "Workspace storage is unavailable or an operation remains unconfirmed.")
	default:
		return false
	}
	return true
}
