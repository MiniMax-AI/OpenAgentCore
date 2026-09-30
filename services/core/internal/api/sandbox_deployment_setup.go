package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

type SandboxDeploymentInput struct {
	ExpectedGeneration *uint64 `json:"expected_generation" binding:"required"`
	// Per-sandbox limits, required for Docker and microsandbox. E2B may omit
	// them; Core then uses the validated template build's cpus and memory_mib.
	Resources     sandbox.Resources       `json:"resources"`
	Runtime       *sandbox.RuntimeRelease `json:"runtime,omitempty"`
	Provider      string                  `json:"provider"`
	Configuration json.RawMessage         `json:"configuration" swaggertype:"object"`
	Credential    json.RawMessage         `json:"credential,omitempty" swaggertype:"object"`
}

type SandboxDeploymentChangeInput struct {
	SandboxDeploymentInput
}

// selection decodes the submitted configuration with the provider's declared codec.
func (h *Handler) selection(v SandboxDeploymentInput) (sandbox.Selection, error) {
	c, err := h.Sandboxes.Deployment.DecodeConfiguration(v.Provider, v.Configuration, v.Credential)
	if err != nil {
		return sandbox.Selection{}, err
	}
	return sandbox.Selection{ExpectedGeneration: *v.ExpectedGeneration, Provider: v.Provider, DeploymentSpec: sandbox.DeploymentSpec{Resources: v.Resources, Runtime: v.Runtime}, Configuration: c}, nil
}

// DeploymentChanges sets up and updates the sandbox deployment through the
// execution owner.
type DeploymentChanges interface {
	InitializeSandboxDeployment(ctx context.Context, input sandbox.Selection) (deployment.View, error)
	UpdateSandboxDeployment(ctx context.Context, input sandbox.Selection) (deployment.View, error)
}

// DeploymentReset starts and cancels a sandbox deployment reset through the
// execution owner.
type DeploymentReset interface {
	StartSandboxReset(ctx context.Context, input deployment.ResetRequest) (deployment.View, error)
	CancelSandboxReset(ctx context.Context, generation uint64) (deployment.View, error)
}

// @Summary Initialize the deployment sandbox provider
// @Description Selects a provider, enforced resource limits and pinned Runtime release. Core derives the deployment's core_url from the installation public URL and rejects a core_url member with 400. E2B returns 409 sandbox_configuration_error while the public URL is loopback. E2B credentials are write-only. E2B may omit resources to adopt the validated template build's CPU and memory, returned in specification.resources. Requires explicit expected_generation, including zero at first setup. Stale retries reject before provider validation. An identical selection at the current generation is a no-op; differing selections and file-managed deployments reject. This does not create compute or execute work.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Accept json
// @Param body body api.SandboxDeploymentInput true "Deployment selection"
// @Success 200 {object} deployment.View
// @Failure 400,401,409,500,503 {object} CoreErrorResponse
// @Router /core/v1/sandbox/deployment [post]
func (h *Handler) initializeSandboxDeployment(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var input SandboxDeploymentInput
	if decodeInputObject(raw, &input, "provider", "configuration", "credential", "resources", "runtime", "expected_generation") != nil || input.ExpectedGeneration == nil {
		writeDeploymentError(w, r, deployment.ErrInvalidInput)
		return
	}
	selection, err := h.selection(input)
	if err != nil {
		writeDeploymentError(w, r, err)
		return
	}
	result, err := h.Sandboxes.DeploymentChanges.InitializeSandboxDeployment(r.Context(), selection)
	if err != nil {
		writeDeploymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// @Summary Change the sandbox deployment configuration
// @Description Requires the observed generation, the same backend type and no active reset. E2B same-team changes apply online: allocations retain immutable generation and current credentials; omitted api_key preserves it, explicit submission including the same key verifies and advances generation. Other teams require explicit reset. Node providers retain the zero-resource guard and retire old nodes/tokens on change. Core rejects core_url input. Never automatically replay an uncertain write; rollout.state is the authoritative preparation polling signal.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Accept json
// @Param body body api.SandboxDeploymentChangeInput true "Replacement deployment selection"
// @Success 200 {object} deployment.View
// @Failure 400,401,409,500,503 {object} CoreErrorResponse
// @Router /core/v1/sandbox/deployment [put]
func (h *Handler) updateSandboxDeployment(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var input SandboxDeploymentChangeInput
	if decodeInputObject(raw, &input, "provider", "configuration", "credential", "resources", "runtime", "expected_generation") != nil || input.ExpectedGeneration == nil {
		writeDeploymentError(w, r, deployment.ErrInvalidInput)
		return
	}
	selection, err := h.selection(input.SandboxDeploymentInput)
	if err != nil {
		writeDeploymentError(w, r, err)
		return
	}
	result, err := h.Sandboxes.DeploymentChanges.UpdateSandboxDeployment(r.Context(), selection)
	if err != nil {
		writeDeploymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// @Summary Start or escalate a durable sandbox deployment reset
// @Description Archives hosted Sessions and waits for confirmed provider cleanup, preserving history and Files/Artifacts. Auto waits for started or waiting Turns and file writes until the durable deadline; force cancels them. The same clear is idempotent; force escalates auto. Requires the current generation. Self-hosted Sessions are unchanged. The response is the current deployment read after the reset commits; resource counts are live and may already differ.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Accept json
// @Param body body deployment.ResetRequest true "Reset mode and current deployment generation"
// @Success 200 {object} deployment.View
// @Failure 400,401,409,500,503 {object} CoreErrorResponse
// @Router /core/v1/sandbox/deployment/reset [post]
func (h *Handler) startSandboxReset(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBodyLimit(w, r, 4096, "Reset request is too large.")
	if !ok {
		return
	}
	var input struct {
		ExpectedGeneration *uint64 `json:"expected_generation"`
		Clear              string  `json:"clear"`
		DeadlineSeconds    *int32  `json:"deadline_seconds"`
	}
	if decodeInputObject(raw, &input, "expected_generation", "clear", "deadline_seconds") != nil || input.ExpectedGeneration == nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "A current expected_generation is required.", "expected_generation")
		return
	}
	if input.Clear != deployment.ResetAuto && input.Clear != deployment.ResetForce {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "Choose auto or force for clear.", "clear")
		return
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if value, present := fields["deadline_seconds"]; present && string(value) == "null" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "deadline_seconds must be an integer when supplied.", "deadline_seconds")
		return
	}
	if input.DeadlineSeconds != nil && (input.Clear != deployment.ResetAuto || *input.DeadlineSeconds < deployment.MinResetDeadlineSeconds || *input.DeadlineSeconds > deployment.MaxResetDeadlineSeconds) {
		message := fmt.Sprintf("deadline_seconds applies only to auto and must be between %d and %d.", deployment.MinResetDeadlineSeconds, deployment.MaxResetDeadlineSeconds)
		writeCoreError(w, http.StatusBadRequest, "invalid_request_error", message, CoreErrorDetails{"min": CoreErrorNumber(deployment.MinResetDeadlineSeconds), "max": CoreErrorNumber(deployment.MaxResetDeadlineSeconds)}, "deadline_seconds")
		return
	}
	setAdminAuditSource(r, "")
	result, err := h.Sandboxes.DeploymentReset.StartSandboxReset(r.Context(), deployment.ResetRequest{ExpectedGeneration: *input.ExpectedGeneration, Clear: input.Clear, DeadlineSeconds: input.DeadlineSeconds})
	if err != nil {
		writeDeploymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// @Summary Cancel a sandbox deployment reset
// @Description Restores admission but never restores Sessions already archived. With no reset running this is an idempotent read, provided the generation still matches. The response is the current deployment read after the cancellation commits.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Param expected_generation query integer true "Current deployment generation"
// @Success 200 {object} deployment.View
// @Failure 400,401,409,500,503 {object} CoreErrorResponse
// @Router /core/v1/sandbox/deployment/reset [delete]
func (h *Handler) cancelSandboxReset(w http.ResponseWriter, r *http.Request) {
	query, err := parseResetGeneration(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "A single current expected_generation is required.", "expected_generation")
		return
	}
	setAdminAuditSource(r, "")
	result, err := h.Sandboxes.DeploymentReset.CancelSandboxReset(r.Context(), query)
	if err != nil {
		writeDeploymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func parseResetGeneration(r *http.Request) (uint64, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 1 || len(query["expected_generation"]) != 1 {
		return 0, deployment.ErrInvalidInput
	}
	return strconv.ParseUint(query.Get("expected_generation"), 10, 64)
}
