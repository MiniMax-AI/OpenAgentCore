package api

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

// @Summary Retrieve sandbox node and host history
// @Description Core key only. Complete UTC buckets. Missing host measurements and offline history are null; reads never sample or backfill.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Param node_id path string true "Sandbox node UUID"
// @Param range query string false "Time range (default 1h)" Enums(1h,6h,24h)
// @Success 200 {object} store.RuntimeNodeDetail
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /core/v1/sandbox/nodes/{node_id} [get]
func (h *Handler) sandboxNodeDetail(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) > 1 || len(values) == 1 && len(values["range"]) != 1 {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	name := "1h"
	if v, ok := values["range"]; ok {
		name = v[0]
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	value, err := h.sandboxStore.GetRuntimeNodeDetail(ctx, chi.URLParam(r, "node_id"), name)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
