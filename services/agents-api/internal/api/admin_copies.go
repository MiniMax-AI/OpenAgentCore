package api

import (
	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type AdminCopyRequest struct {
	SourceProjectID     string `json:"source_project_id"`
	TargetProjectID     string `json:"target_project_id"`
	ResourceType        string `json:"resource_type"`
	ResourceID          string `json:"resource_id"`
	IncludeDependencies bool   `json:"include_dependencies"`
	TargetVaultID       string `json:"target_vault_id,omitempty"`
}

// @Summary Copy assets into another Project
// @Description Deployment administrator only. Copies content and allowed secrets inside one transaction, rewrites selected dependencies and records administrator provenance. Refreshable OAuth credentials are skipped. Sessions and Artifacts cannot be copied. Reusing an Idempotency-Key with changed input returns conflict.
// @Tags Core Administration
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param Idempotency-Key header string false "Copy retry identity, up to 128 characters"
// @Param body body api.AdminCopyRequest true "Source and target Projects and resource"
// @Success 200 {object} store.CopyAssetsResult
// @Failure 400,401,404,409,500 {object} v1.ErrorResponse
// @Router /core/v1/admin/copies [post]
func (h *Handler) copyAdminAssets(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBodyLimit(w, r, 4096, "Copy request is too large.")
	if !ok {
		return
	}
	var input AdminCopyRequest
	if decodeInputObject(raw, &input, "source_project_id", "target_project_id", "resource_type", "resource_id", "include_dependencies", "target_vault_id") != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(r.Header.Values("Idempotency-Key")) > 1 || len(key) > 128 || strings.TrimSpace(key) != key {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	source, err := h.resolveAdminProject(r.Context(), input.SourceProjectID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	target, err := h.resolveAdminProject(r.Context(), input.TargetProjectID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	ctx := h.adminAuditContext(r, input.TargetProjectID)
	result, err := h.adminManagement.CopyAssets(ctx, source.Principal.TenantID, target.Principal.TenantID, store.CopyAssetsInput{
		ResourceType: input.ResourceType, ResourceID: input.ResourceID, TargetVaultID: input.TargetVaultID, IncludeDependencies: input.IncludeDependencies, IdempotencyKey: key,
	})
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
