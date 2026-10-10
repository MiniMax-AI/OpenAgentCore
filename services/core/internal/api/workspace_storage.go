package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspaces"
)

// WorkspaceStorage reads and changes the independent filesystem configuration.
// Configure serializes selection changes and records the admin audit source.
type WorkspaceStorage interface {
	Configuration(context.Context) (workspacefs.Configuration, error)
	Configure(context.Context, workspacefs.Configuration) (workspacefs.Configuration, error)
}

// @Summary Retrieve workspace storage configuration
// @Description Core key only. Returns the immutable selected filesystem configuration. No configuration returns 404.
// @Tags Workspace Storage
// @Produce json
// @Security DeploymentAdminAuth
// @Success 200 {object} workspacefs.Configuration
// @Failure 400,401,404,409,500,503 {object} CoreErrorResponse
// @Router /core/v1/workspace-storage [get]
func (h *Handler) getWorkspaceStorage(w http.ResponseWriter, r *http.Request) {
	configuration, err := h.WorkspaceStorage.Configuration(r.Context())
	if err != nil {
		writeWorkspaceStorageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, configuration)
}

// @Summary Select workspace storage configuration
// @Description Core key only. Selects an immutable filesystem configuration. Reusing an ID requires the same adapter and parameters; conflicting ownership returns 409.
// @Tags Workspace Storage
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param body body workspacefs.Configuration true "Immutable workspace storage configuration"
// @Success 200 {object} workspacefs.Configuration
// @Failure 400,401,404,409,500,503 {object} CoreErrorResponse
// @Router /core/v1/workspace-storage [put]
func (h *Handler) putWorkspaceStorage(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBodyLimit(w, r, 16384, "Workspace storage configuration is too large.")
	if !ok {
		return
	}
	var configuration workspacefs.Configuration
	if decodeInputObject(raw, &configuration, "id", "adapter", "parameters") != nil || configuration.Validate() != nil {
		writeWorkspaceStorageError(w, r, workspacefs.ErrInvalid)
		return
	}
	setAdminAuditSource(r, "")
	configuration, err := h.WorkspaceStorage.Configure(r.Context(), configuration)
	if err != nil {
		writeWorkspaceStorageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, configuration)
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
