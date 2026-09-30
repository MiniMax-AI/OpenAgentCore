package api

import (
	"context"
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/go-chi/chi/v5"
)

// SessionArchive archives a managed Session through the execution owner;
// SessionAdmin reads its archive state.
type SessionArchive interface {
	ArchiveManagedSession(context.Context, string, string, uint64) (store.ManagedSessionArchive, error)
}

type AdminSessionArchiveRequest struct {
	ExpectedGeneration uint64 `json:"expected_generation"`
}

// @Summary Release a managed Session's execution resources while retaining history
// @Description Core key only. Requires the current deployment generation; no maintenance mode is required. Permanently closes execution, requests cancellation and releases sandbox/snapshots through existing cleanup. Session history and persisted files/artifacts remain; unpersisted workspace contents are lost. A cleanup_pending response is not proof of resource release. Does not affect caller-managed Runtime.
// @Tags Core Administration
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param project_id path string true "Project ID"
// @Param session_id path string true "Session ID"
// @Param body body api.AdminSessionArchiveRequest true "Current deployment generation"
// @Success 200 {object} store.ManagedSessionArchive
// @Failure 400,401,404,409,413,500,503 {object} CoreErrorResponse
// @Router /core/v1/projects/{project_id}/sessions/{session_id}/archive [post]
func (h *Handler) adminArchiveSession(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBodyLimit(w, r, 4096, "Archive request is too large.")
	if !ok {
		return
	}
	var input AdminSessionArchiveRequest
	if decodeInputObject(raw, &input, "expected_generation") != nil || input.ExpectedGeneration == 0 {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	if h.Execution == nil {
		writeStoreError(w, r, store.ErrEnvironmentUnavailable)
		return
	}
	result, err := h.Execution.SessionArchive.ArchiveManagedSession(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), input.ExpectedGeneration)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// @Summary Retrieve a managed Session's resource cleanup state
// @Description Core key only. Reports actual resource disposition, including expiry and failure cleanup. This is not archive provenance and does not assert active Turn settlement. Read this after an uncertain archive response; never infer released from a missing sandbox alone.
// @Tags Core Administration
// @Produce json
// @Security DeploymentAdminAuth
// @Param project_id path string true "Project ID"
// @Param session_id path string true "Session ID"
// @Success 200 {object} store.ManagedSessionArchive
// @Failure 400,401,404,500,503 {object} CoreErrorResponse
// @Router /core/v1/projects/{project_id}/sessions/{session_id}/archive [get]
func (h *Handler) adminGetSessionArchive(w http.ResponseWriter, r *http.Request) {
	result, err := h.SessionAdmin.GetManagedSessionArchive(r.Context(), tenantID(r), chi.URLParam(r, "session_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
