package api

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// @Summary Retrieve a sandbox node and its capacity recommendation
// @Description Administrator-only observation. Registration does not enable scheduling. Missing measurements are null.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Param node_id path string true "Sandbox node UUID"
// @Success 200 {object} store.RuntimeNode
// @Failure 400,401,404,409,500,503 {object} v1.ErrorResponse
// @Router /core/v1/sandbox/nodes/{node_id} [get]
func (h *Handler) sandboxNode(w http.ResponseWriter, r *http.Request) {
	value, err := h.sandboxStore.GetRuntimeNode(r.Context(), chi.URLParam(r, "node_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

// @Summary Observe a node installation's enrollment receipt
// @Description Administrator-only receipt identifies the exact enrolled node. Never returns enrollment credentials.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Param enrollment_id path string true "Enrollment UUID"
// @Success 200 {object} store.RuntimeEnrollmentReceipt
// @Failure 400,401,404,409,500,503 {object} v1.ErrorResponse
// @Router /core/v1/sandbox/enrollment-tokens/{enrollment_id} [get]
func (h *Handler) sandboxEnrollment(w http.ResponseWriter, r *http.Request) {
	value, err := h.sandboxStore.GetRuntimeEnrollmentReceipt(r.Context(), chi.URLParam(r, "enrollment_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

// Omission preserves a PATCH field; explicit null is not a capacity or name.
func nodeUpdateHasNull(raw []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return true
	}
	for _, key := range []string{"name", "max_active", "max_retained", "admission_state", "expected_config_revision"} {
		if bytes.Equal(bytes.TrimSpace(fields[key]), []byte("null")) {
			return true
		}
	}
	return false
}
