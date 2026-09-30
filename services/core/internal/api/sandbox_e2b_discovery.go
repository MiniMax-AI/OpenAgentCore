package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/go-chi/chi/v5"
)

// Discovery credentials are transient and never included in the response.
type SandboxE2BDiscoveryInput struct {
	APIKey string `json:"api_key"`
	APIURL string `json:"api_url,omitempty"`
	Domain string `json:"domain,omitempty"`
}
type SandboxE2BDiscoveryResult struct {
	Templates []e2b.TemplateSummary `json:"templates"`
	Builds    []e2b.ReadyBuild      `json:"builds"`
}

func WithSandboxE2BDiscovery(discover func(context.Context, SandboxE2BDiscoveryInput, string) (SandboxE2BDiscoveryResult, error)) Option {
	return func(h *Handler) { h.sandboxE2BDiscover = discover }
}

// @Summary List templates visible to an E2B credential
// @Description Core key only. Uses a transient E2B credential and endpoint through the pinned SDK helper; returns safe template metadata. Does not save the credential or allocate compute.
// @Tags Sandbox Manager
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param body body api.SandboxE2BDiscoveryInput true "Transient E2B connection"
// @Success 200 {object} api.SandboxE2BDiscoveryResult
// @Failure 400,401,503 {object} CoreErrorResponse
// @Router /core/v1/sandbox/e2b/templates [post]
func (h *Handler) discoverSandboxE2BTemplates(w http.ResponseWriter, r *http.Request) {
	h.discoverSandboxE2B(w, r, "")
}

// @Summary List ready builds for an E2B template
// @Description Core key only. Reads one template through the pinned SDK helper with a transient E2B credential. Returns ready builds only, without allocating compute.
// @Tags Sandbox Manager
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param template_id path string true "Template ID"
// @Param body body api.SandboxE2BDiscoveryInput true "Transient E2B connection"
// @Success 200 {object} api.SandboxE2BDiscoveryResult
// @Failure 400,401,503 {object} CoreErrorResponse
// @Router /core/v1/sandbox/e2b/templates/{template_id}/builds [post]
func (h *Handler) discoverSandboxE2BBuilds(w http.ResponseWriter, r *http.Request) {
	h.discoverSandboxE2B(w, r, chi.URLParam(r, "template_id"))
}

func (h *Handler) discoverSandboxE2B(w http.ResponseWriter, r *http.Request, template string) {
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var input SandboxE2BDiscoveryInput
	if decodeInputObject(raw, &input, "api_key", "api_url", "domain") != nil || h.sandboxE2BDiscover == nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "Invalid E2B discovery request.", "")
		return
	}
	result, err := h.sandboxE2BDiscover(r.Context(), input, template)
	if err != nil {
		if errors.Is(err, sandbox.ErrInvalid) {
			writeError(w, http.StatusBadRequest, "invalid_request_error", "The E2B credential, endpoint, or template was rejected.", "")
		} else {
			writeError(w, http.StatusServiceUnavailable, "provider_unavailable", "E2B template discovery is unavailable. Check the credential, endpoint and provider connection, then retry.", "")
		}
		return
	}
	if template == "" {
		writeJSON(w, http.StatusOK, struct {
			Templates []e2b.TemplateSummary `json:"templates"`
		}{Templates: result.Templates})
	} else {
		writeJSON(w, http.StatusOK, struct {
			Builds []e2b.ReadyBuild `json:"builds"`
		}{Builds: result.Builds})
	}
}
