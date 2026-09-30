package api

import (
	"math"
	"net/http"
	"strconv"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
)

// @Summary Read the active configuration for node installation
// @Description Authenticates with an unconsumed enrollment token, or a retained node credential with X-OAC-Node-ID. Does not consume the token or expose E2B credentials. Node files cannot override this specification.
// @Tags Sandbox Node
// @Produce json
// @Security NodeEnrollmentAuth
// @Param X-OAC-Node-ID header string false "Retained node UUID"
// @Param generation query integer false "Exact kept generation (registered nodes only); omitted reads the current target"
// @Success 200 {object} deployment.NodeConfiguration
// @Failure 400,401,409,500,503 {object} v1.ErrorResponse
// @Router /api/v1/sandbox-node/configuration [get]
func (h *Handler) sandboxNodeConfiguration(w http.ResponseWriter, r *http.Request) {
	token, ok := sandboxBearer(r)
	if !ok || len(r.Header.Values("X-OAC-Node-ID")) > 1 {
		writeDeploymentError(w, r, deployment.ErrNodeCredential)
		return
	}
	var generation uint64
	if values, present := r.URL.Query()["generation"]; present {
		var err error
		if len(values) != 1 {
			writeDeploymentError(w, r, deployment.ErrInvalidInput)
			return
		}
		generation, err = strconv.ParseUint(values[0], 10, 63)
		if err != nil || generation == 0 || generation > math.MaxInt64 || strconv.FormatUint(generation, 10) != values[0] {
			writeDeploymentError(w, r, deployment.ErrInvalidInput)
			return
		}
	}
	value, err := h.Sandboxes.Deployment.NodeConfiguration(r.Context(), r.Header.Get("X-OAC-Node-ID"), token, generation)
	if err != nil {
		writeDeploymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
