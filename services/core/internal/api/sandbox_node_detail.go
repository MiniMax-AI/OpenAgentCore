package api

import (
	"context"
	"net/http"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/go-chi/chi/v5"
)

// @Summary Retrieve sandbox node and host history
// @Description Core key only. Complete UTC buckets. Missing host measurements and offline history are null; reads never sample or backfill.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Param node_id path string true "Sandbox node UUID"
// @Param range query string false "Time range (default 1h)" Enums(1h,6h,24h)
// @Success 200 {object} deployment.NodeDetail
// @Failure 400,401,404,500,503 {object} CoreErrorResponse
// @Router /core/v1/sandbox/nodes/{node_id} [get]
func (h *Handler) sandboxNodeDetail(w http.ResponseWriter, r *http.Request) {
	name, ok := rangeName(r)
	if !ok {
		writeDeploymentError(w, r, deployment.ErrInvalidInput)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	value, err := h.Sandboxes.Deployment.NodeDetail(ctx, chi.URLParam(r, "node_id"), name)
	if err != nil {
		writeDeploymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
