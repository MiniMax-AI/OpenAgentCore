package api

import (
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"net/http"
)

// @Summary Read the active configuration for node installation
// @Description Authenticates with an unconsumed enrollment token, or a retained node credential with X-OAC-Node-ID. Does not consume the token or expose E2B credentials. Node files cannot override this specification.
// @Tags Sandbox Node
// @Produce json
// @Security NodeEnrollmentAuth
// @Param X-OAC-Node-ID header string false "Retained node UUID"
// @Success 200 {object} store.RuntimeNodeConfiguration
// @Failure 400,401,409,500,503 {object} v1.ErrorResponse
// @Router /api/v1/sandbox-node/configuration [get]
func (h *Handler) sandboxNodeConfiguration(w http.ResponseWriter, r *http.Request) {
	if _, present := r.Header[http.CanonicalHeaderKey("X-Parsar-Node-ID")]; present {
		writeError(w, http.StatusBadRequest, "invalid_request", "X-Parsar-Node-ID was renamed to X-OAC-Node-ID; use the node command from this Core's Web")
		return
	}

	token, ok := sandboxBearer(r)
	if !ok || len(r.Header.Values("X-OAC-Node-ID")) > 1 {
		writeStoreError(w, r, store.ErrRuntimeNodeCredential)
		return
	}
	value, err := h.sandboxStore.RuntimeNodeConfiguration(r.Context(), r.Header.Get("X-OAC-Node-ID"), token)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
