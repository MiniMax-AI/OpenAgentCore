package api

import (
	"context"
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
	Provider string           `json:"provider"`
	CoreURL  string           `json:"core_url"`
	E2B      *SandboxE2BInput `json:"e2b,omitempty"`
}

type SandboxDeploymentChangeInput struct {
	SandboxDeploymentInput
	ExpectedGeneration uint64 `json:"expected_generation"`
}

func (v SandboxDeploymentInput) request() store.SandboxDeploymentSetupRequest {
	input := store.SandboxDeploymentSetupRequest{Provider: v.Provider, CoreURL: v.CoreURL}
	if v.E2B != nil {
		input.E2B = &store.SandboxE2BConfiguration{APIKey: v.E2B.APIKey, Template: v.E2B.Template}
	}
	return input
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
// @Description Selects a provider and public Core origin. E2B credentials are write-only. Exact retries return the existing selection; differing selections and file-managed deployments reject. This does not create compute or execute work.
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
	var input SandboxDeploymentInput
	if decodeInputObject(raw, &input, "provider", "core_url", "e2b") != nil {
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

// @Summary Change a fully drained deployment's sandbox provider
// @Description Requires maintenance, the current generation and verified cleanup of all old resources. Credentials are write-only. The public Core origin stays unchanged. Historical records are retained; old node credentials and enrollments are retired. Explicitly resume after success. Never automatically retry an uncertain write.
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
	var input SandboxDeploymentChangeInput
	if decodeInputObject(raw, &input, "provider", "core_url", "e2b", "expected_generation") != nil || input.ExpectedGeneration == 0 {
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
