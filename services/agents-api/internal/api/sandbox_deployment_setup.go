package api

import (
	"context"
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func WithSandboxDeploymentSetup(initialize func(context.Context, store.SandboxDeploymentSetupRequest) (store.RuntimeDeploymentView, error)) Option {
	return func(h *Handler) { h.sandboxSetup = initialize }
}

// @Summary Initialize the deployment sandbox provider once
// @Description Selects one immutable provider and public Core origin. Exact retries return the existing selection; differing selections and file-managed deployments reject. This does not create a node or execute work.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Accept json
// @Param body body store.SandboxDeploymentSetupRequest true "Deployment selection"
// @Success 200 {object} store.RuntimeDeploymentView
// @Failure 400,401,409,500,503 {object} v1.ErrorResponse
// @Router /core/v1/sandbox/deployment [post]
func (h *Handler) initializeSandboxDeployment(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var input store.SandboxDeploymentSetupRequest
	if decodeInputObject(raw, &input, "provider", "core_url") != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	if h.sandboxSetup == nil {
		writeStoreError(w, r, store.ErrSandboxDeploymentConflict)
		return
	}
	result, err := h.sandboxSetup(r.Context(), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
