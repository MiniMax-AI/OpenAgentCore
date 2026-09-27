package api

import (
	"context"
	"encoding/json"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// SandboxE2BInput is write-only provider configuration. Safe responses use the
// store's separate deployment view and never serialize this request.
type SandboxE2BInput struct {
	APIKey   string `json:"api_key"`
	Template string `json:"template"`
}

type SandboxDeploymentInput struct {
	// Per-sandbox limits, required for Docker and microsandbox. E2B may omit
	// them; Core then uses the validated template build's cpus and memory_mib.
	Resources sandbox.Resources       `json:"resources"`
	Runtime   *sandbox.RuntimeRelease `json:"runtime,omitempty"`
	Provider  string                  `json:"provider"`
	E2B       *SandboxE2BInput        `json:"e2b,omitempty"`
}

type SandboxDeploymentChangeInput struct {
	SandboxDeploymentInput
	ExpectedGeneration uint64 `json:"expected_generation"`
}

func (v SandboxDeploymentInput) request() store.SandboxDeploymentSetupRequest {
	input := store.SandboxDeploymentSetupRequest{Provider: v.Provider, DeploymentSpec: sandbox.DeploymentSpec{Resources: v.Resources, Runtime: v.Runtime}}
	if v.E2B != nil {
		input.E2B = &store.SandboxE2BConfiguration{APIKey: v.E2B.APIKey, Template: v.E2B.Template}
	}
	return input
}

// rejectCoreURL names the retired member instead of reporting a generic unknown
// member: Core derives core_url from the installation public URL.
func rejectCoreURL(w http.ResponseWriter, raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	if _, present := fields["core_url"]; !present {
		return false
	}
	writeError(w, http.StatusBadRequest, "invalid_request_error", "core_url is derived from the installation public URL (public_url in config.json, OAC_PUBLIC_URL for Core) and cannot be set here. Remove it.", "core_url")
	return true
}

func WithSandboxDeploymentSetup(initialize func(context.Context, store.SandboxDeploymentSetupRequest) (store.RuntimeDeploymentView, error)) Option {
	return func(h *Handler) { h.sandboxSetup = initialize }
}

func WithSandboxDeploymentChanges(
	update func(context.Context, store.SandboxDeploymentUpdateRequest) (store.RuntimeDeploymentView, error),
	maintenance func(context.Context, store.SandboxMaintenanceRequest) (store.RuntimeDeploymentView, error),
) Option {
	return func(h *Handler) { h.sandboxUpdate = update; h.sandboxMaintenance = maintenance }
}

// @Summary Initialize the deployment sandbox provider
// @Description Selects a provider, enforced resource limits and pinned Runtime release. Core derives the deployment's core_url from the installation public URL and rejects a core_url member with 400. E2B returns 409 sandbox_configuration_error while the public URL is loopback. E2B credentials are write-only. E2B may omit resources to adopt the validated template build's CPU and memory, returned in specification.resources. Exact retries return the existing selection; differing selections and file-managed deployments reject. This does not create compute or execute work.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Accept json
// @Param body body api.SandboxDeploymentInput true "Deployment selection"
// @Success 200 {object} store.RuntimeDeploymentView
// @Failure 400,401,409,500,503 {object} v1.ErrorResponse
// @Router /core/v1/sandbox/deployment [post]
func (h *Handler) initializeSandboxDeployment(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	if rejectCoreURL(w, raw) {
		return
	}
	var input SandboxDeploymentInput
	if decodeInputObject(raw, &input, "provider", "e2b", "resources", "runtime") != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	if h.sandboxSetup == nil {
		writeStoreError(w, r, store.ErrSandboxDeploymentConflict)
		return
	}
	result, err := h.sandboxSetup(r.Context(), input.request())
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// @Summary Change a fully drained deployment's sandbox configuration
// @Description Requires maintenance, the current generation and verified cleanup of all old resources. Credentials are write-only. E2B may omit resources to adopt the validated template build's CPU and memory. A core_url member is rejected with 400; the address comes from the installation public URL. Historical records are retained; old node credentials and enrollments are retired. Explicitly resume after success. Never automatically retry an uncertain write.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Accept json
// @Param body body api.SandboxDeploymentChangeInput true "Replacement deployment selection"
// @Success 200 {object} store.RuntimeDeploymentView
// @Failure 400,401,409,500,503 {object} v1.ErrorResponse
// @Router /core/v1/sandbox/deployment [put]
func (h *Handler) updateSandboxDeployment(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	if rejectCoreURL(w, raw) {
		return
	}
	var input SandboxDeploymentChangeInput
	if decodeInputObject(raw, &input, "provider", "e2b", "resources", "runtime", "expected_generation") != nil || input.ExpectedGeneration == 0 {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	if h.sandboxUpdate == nil {
		writeStoreError(w, r, store.ErrSandboxDeploymentConflict)
		return
	}
	result, err := h.sandboxUpdate(r.Context(), store.SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: input.request(), ExpectedGeneration: input.ExpectedGeneration})
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// @Summary Pause or resume new hosted allocations
// @Description Requires the current generation. Maintenance preserves execution, cleanup and history. Resume requires successfully activated provider configuration.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Accept json
// @Param body body store.SandboxMaintenanceRequest true "Maintenance state"
// @Success 200 {object} store.RuntimeDeploymentView
// @Failure 400,401,409,500,503 {object} v1.ErrorResponse
// @Router /core/v1/sandbox/deployment/maintenance [patch]
func (h *Handler) setSandboxMaintenance(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	// Omitted or null maintenance is not an instruction to resume.
	var input struct {
		Maintenance        *bool  `json:"maintenance"`
		ExpectedGeneration uint64 `json:"expected_generation"`
	}
	if decodeInputObject(raw, &input, "maintenance", "expected_generation") != nil || input.Maintenance == nil || input.ExpectedGeneration == 0 {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	if h.sandboxMaintenance == nil {
		writeStoreError(w, r, store.ErrSandboxDeploymentConflict)
		return
	}
	result, err := h.sandboxMaintenance(r.Context(), store.SandboxMaintenanceRequest{Maintenance: *input.Maintenance, ExpectedGeneration: input.ExpectedGeneration})
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
