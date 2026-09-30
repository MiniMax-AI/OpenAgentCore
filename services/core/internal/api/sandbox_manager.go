package api

import (
	"net/http"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/go-chi/chi/v5"
)

type SandboxNodeList struct {
	Data []store.RuntimeNode `json:"data"`
}
type SandboxAllocationList struct {
	Data []store.RuntimeNodeAllocation `json:"data"`
}
type SandboxEnrollmentToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	// Public, non-secret handle of this command; never a credential. The node it registers reports the same value as enrollment_id.
	EnrollmentID string `json:"enrollment_id"`
}
type SandboxMutationResponse struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted,omitempty"`
	Updated bool   `json:"updated,omitempty"`
}
type SandboxEnrollmentTokenRequest struct {
	MaxActive *int `json:"max_active,omitempty"`
	// Docker never suspends, so Core replaces this with max_active; microsandbox uses both limits.
	MaxRetained *int `json:"max_retained,omitempty"`
}

func WithSandboxManager(s *store.Store, auth *DeploymentAuthenticator) Option {
	return func(h *Handler) { h.sandboxStore = s; h.deploymentAuth = auth }
}

// registerSandboxNodeRoutes serves node machine connections. They authenticate
// with an enrollment token or node credential, never the Core key.
func (h *Handler) registerSandboxNodeRoutes(r chi.Router) {
	if h.sandboxStore == nil {
		return
	}
	r.Post("/api/v1/sandbox-node/enroll", h.enrollSandboxNode)
	r.Get("/api/v1/sandbox-node/identity", h.sandboxNodeIdentity)
	r.Get("/api/v1/sandbox-node/configuration", h.sandboxNodeConfiguration)
}

// registerSandboxManagerRoutes adds sandbox deployment and node administration
// to the Core-key-authenticated /core/v1 router.
func (h *Handler) registerSandboxManagerRoutes(r chi.Router) {
	if h.sandboxStore == nil {
		return
	}
	r.Get("/sandbox/deployment", h.sandboxDeployment)
	r.Post("/sandbox/e2b/templates", h.discoverSandboxE2BTemplates)
	r.Post("/sandbox/e2b/templates/{template_id}/builds", h.discoverSandboxE2BBuilds)
	r.Post("/sandbox/deployment", h.initializeSandboxDeployment)
	r.Put("/sandbox/deployment", h.updateSandboxDeployment)
	r.Post("/sandbox/deployment/reset", h.startSandboxReset)
	r.Delete("/sandbox/deployment/reset", h.cancelSandboxReset)
	r.Get("/sandbox/nodes", h.sandboxNodes)
	r.Get("/sandbox/nodes/{node_id}", h.sandboxNodeDetail)
	r.Patch("/sandbox/nodes/{node_id}", h.updateSandboxNode)
	r.Delete("/sandbox/nodes/{node_id}", h.removeSandboxNode)
	r.Get("/sandbox/nodes/{node_id}/allocations", h.sandboxAllocations)
	r.Post("/sandbox/enrollment-tokens", h.createSandboxEnrollment)
}

// @Summary Retrieve sandbox deployment
// @Description Core key only. Does not grant project resource access. Responses contain only explicit safe fields. E2B template_build values are those Core read when the selection was saved; this read does not call E2B.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Success 200 {object} store.RuntimeDeploymentView
// @Failure 400,401,404,409,500,503 {object} CoreErrorResponse
// @Router /core/v1/sandbox/deployment [get]
func (h *Handler) sandboxDeployment(w http.ResponseWriter, r *http.Request) {
	value, err := h.sandboxStore.GetRuntimeDeployment(r.Context())
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

// @Summary List deployment sandbox nodes
// @Description Core key only. Does not grant project resource access. Responses contain only explicit safe fields.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Success 200 {object} api.SandboxNodeList
// @Failure 400,401,404,409,500,503 {object} CoreErrorResponse
// @Router /core/v1/sandbox/nodes [get]
func (h *Handler) sandboxNodes(w http.ResponseWriter, r *http.Request) {
	value, err := h.sandboxStore.ListRuntimeNodes(r.Context())
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, SandboxNodeList{Data: value})
}

// @Summary Update sandbox node name and capacity
// @Description Core key only. Does not grant project resource access. Responses contain only explicit safe fields.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Param node_id path string true "Sandbox node UUID"
// @Accept json
// @Param body body store.RuntimeNodeUpdate true "Request"
// @Success 200 {object} api.SandboxMutationResponse
// @Failure 400,401,404,409,500,503 {object} CoreErrorResponse
// @Router /core/v1/sandbox/nodes/{node_id} [patch]
func (h *Handler) updateSandboxNode(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var input store.RuntimeNodeUpdate
	if decodeInputObject(raw, &input, "name", "max_active", "max_retained") != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	id := chi.URLParam(r, "node_id")
	if err := h.sandboxStore.UpdateRuntimeNode(r.Context(), id, input); err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, SandboxMutationResponse{ID: id, Updated: true})
}

// @Summary Remove a sandbox node with no retained resources
// @Description Core key only. Does not grant project resource access. Responses contain only explicit safe fields.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Param node_id path string true "Sandbox node UUID"
// @Success 200 {object} api.SandboxMutationResponse
// @Failure 400,401,404,409,500,503 {object} CoreErrorResponse
// @Router /core/v1/sandbox/nodes/{node_id} [delete]
func (h *Handler) removeSandboxNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "node_id")
	if err := h.sandboxStore.RemoveRuntimeNode(r.Context(), id); err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, SandboxMutationResponse{ID: id, Deleted: true})
}

// @Summary List retained allocations on a sandbox node
// @Description Core key only. Does not grant project resource access. Responses contain only explicit safe fields.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Param node_id path string true "Sandbox node UUID"
// @Success 200 {object} api.SandboxAllocationList
// @Failure 400,401,404,409,500,503 {object} CoreErrorResponse
// @Router /core/v1/sandbox/nodes/{node_id}/allocations [get]
func (h *Handler) sandboxAllocations(w http.ResponseWriter, r *http.Request) {
	value, err := h.sandboxStore.ListNodeRuntimeAllocations(r.Context(), chi.URLParam(r, "node_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, SandboxAllocationList{Data: value})
}

// @Summary Create a ten-minute one-use node enrollment token
// @Description Core key only. Does not grant project resource access. Responses contain only explicit safe fields.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Accept json
// @Param body body api.SandboxEnrollmentTokenRequest true "Request"
// @Success 201 {object} api.SandboxEnrollmentToken
// @Failure 400,401,404,409,500,503 {object} CoreErrorResponse
// @Router /core/v1/sandbox/enrollment-tokens [post]
func (h *Handler) createSandboxEnrollment(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var input SandboxEnrollmentTokenRequest
	if decodeInputObject(raw, &input, "max_active", "max_retained") != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	capacity := store.RuntimeNodeCapacity{MaxActive: 2, MaxRetained: 8}
	if input.MaxActive != nil {
		capacity.MaxActive = *input.MaxActive
	}
	if input.MaxRetained != nil {
		capacity.MaxRetained = *input.MaxRetained
	}
	enrollment, err := h.sandboxStore.CreateRuntimeEnrollment(r.Context(), capacity)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, SandboxEnrollmentToken{Token: enrollment.Token, ExpiresAt: enrollment.ExpiresAt, EnrollmentID: enrollment.ID})
}

// @Summary Enroll a sandbox node
// @Description Node machine connection. Consumes a one-use enrollment token; grants no project or administrator access. Responses contain only explicit safe fields. core_url is required and must equal the installation public URL; a different address gets 409 sandbox_node_address_mismatch and leaves the token unused.
// @Tags Sandbox Node
// @Produce json
// @Security NodeEnrollmentAuth
// @Accept json
// @Param body body store.RuntimeNodeEnrollment true "Request"
// @Success 201 {object} store.RuntimeNodeIdentity
// @Failure 400,401,404,409,500,503 {object} v1.ErrorResponse
// @Router /api/v1/sandbox-node/enroll [post]
func (h *Handler) enrollSandboxNode(w http.ResponseWriter, r *http.Request) {
	token, ok := sandboxBearer(r)
	if !ok {
		writeStoreError(w, r, store.ErrRuntimeNodeCredential)
		return
	}
	raw, ok := readJSONBodyLimit(w, r, 16384, "Sandbox node enrollment is too large.")
	if !ok {
		return
	}
	var input store.RuntimeNodeEnrollment
	if decodeInputObject(raw, &input, "node_id", "credential", "name", "provider", "backend_fingerprint", "deployment_generation", "specification_digest", "core_url") != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	if input.CoreURL == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "Enrollment requires core_url, the Core origin this node uses. Install the node with this Core's node installer.", "core_url")
		return
	}
	value, err := h.sandboxStore.EnrollRuntimeNode(r.Context(), token, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

// @Summary Recover an enrolled sandbox node identity and observe its readiness
// @Description Node machine connection. Authenticates with the retained node credential; grants no project or administrator access. Responses contain only explicit safe fields.
// @Tags Sandbox Node
// @Produce json
// @Security NodeAuth
// @Param node_id query string true "Sandbox node UUID"
// @Success 200 {object} store.RuntimeNodeStatus
// @Failure 400,401,404,409,500,503 {object} v1.ErrorResponse
// @Router /api/v1/sandbox-node/identity [get]
func (h *Handler) sandboxNodeIdentity(w http.ResponseWriter, r *http.Request) {
	token, ok := sandboxBearer(r)
	if !ok {
		writeStoreError(w, r, store.ErrRuntimeNodeCredential)
		return
	}
	ids := r.URL.Query()["node_id"]
	if len(ids) != 1 {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	value, err := h.sandboxStore.RuntimeNodeStatus(r.Context(), ids[0], token)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
