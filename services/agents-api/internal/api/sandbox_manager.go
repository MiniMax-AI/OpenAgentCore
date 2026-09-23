package api

import (
	"net/http"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
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
}
type SandboxMutationResponse struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted,omitempty"`
	Updated bool   `json:"updated,omitempty"`
}
type SandboxEnrollmentTokenRequest struct{}

func WithSandboxManager(s *store.Store, auth *DeploymentAuthenticator) Option {
	return func(h *Handler) { h.sandboxStore = s; h.deploymentAuth = auth }
}
func (h *Handler) registerSandboxManagerRoutes(r chi.Router) {
	if h.sandboxStore == nil {
		return
	}
	r.Post("/core/v1/sandbox/enroll", h.enrollSandboxNode)
	r.Get("/core/v1/sandbox/node/identity", h.sandboxNodeIdentity)
	if h.deploymentAuth == nil {
		return
	}
	r.Route("/core/v1/sandbox", func(r chi.Router) {
		r.Use(h.deploymentAuth.authenticate)
		r.Get("/deployment", h.sandboxDeployment)
		r.Post("/deployment", h.initializeSandboxDeployment)
		r.Get("/nodes", h.sandboxNodes)
		r.Patch("/nodes/{node_id}", h.updateSandboxNode)
		r.Delete("/nodes/{node_id}", h.removeSandboxNode)
		r.Get("/nodes/{node_id}/allocations", h.sandboxAllocations)
		r.Post("/enrollment-tokens", h.createSandboxEnrollment)
	})
}

// @Summary Retrieve sandbox deployment
// @Description Core deployment extension. Does not grant project resource access. Responses contain only explicit safe fields.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Success 200 {object} store.RuntimeDeploymentView
// @Failure 400,401,404,409,500,503 {object} v1.ErrorResponse
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
// @Description Core deployment extension. Does not grant project resource access. Responses contain only explicit safe fields.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Success 200 {object} api.SandboxNodeList
// @Failure 400,401,404,409,500,503 {object} v1.ErrorResponse
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
// @Description Core deployment extension. Does not grant project resource access. Responses contain only explicit safe fields.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Param node_id path string true "Sandbox node UUID"
// @Accept json
// @Param body body store.RuntimeNodeUpdate true "Request"
// @Success 200 {object} api.SandboxMutationResponse
// @Failure 400,401,404,409,500,503 {object} v1.ErrorResponse
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
// @Description Core deployment extension. Does not grant project resource access. Responses contain only explicit safe fields.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Param node_id path string true "Sandbox node UUID"
// @Success 200 {object} api.SandboxMutationResponse
// @Failure 400,401,404,409,500,503 {object} v1.ErrorResponse
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
// @Description Core deployment extension. Does not grant project resource access. Responses contain only explicit safe fields.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Param node_id path string true "Sandbox node UUID"
// @Success 200 {object} api.SandboxAllocationList
// @Failure 400,401,404,409,500,503 {object} v1.ErrorResponse
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
// @Description Core deployment extension. Does not grant project resource access. Responses contain only explicit safe fields.
// @Tags Sandbox Manager
// @Produce json
// @Security DeploymentAdminAuth
// @Accept json
// @Param body body api.SandboxEnrollmentTokenRequest true "Request"
// @Success 201 {object} api.SandboxEnrollmentToken
// @Failure 400,401,404,409,500,503 {object} v1.ErrorResponse
// @Router /core/v1/sandbox/enrollment-tokens [post]
func (h *Handler) createSandboxEnrollment(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var input struct{}
	if decodeInputObject(raw, &input) != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	token, expires, err := h.sandboxStore.CreateRuntimeEnrollment(r.Context())
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, SandboxEnrollmentToken{Token: token, ExpiresAt: expires})
}

// @Summary Enroll a sandbox node
// @Description Core deployment extension. Does not grant project resource access. Responses contain only explicit safe fields.
// @Tags Sandbox Manager
// @Produce json
// @Security NodeEnrollmentAuth
// @Accept json
// @Param body body store.RuntimeNodeEnrollment true "Request"
// @Success 201 {object} store.RuntimeNodeIdentity
// @Failure 400,401,404,409,500,503 {object} v1.ErrorResponse
// @Router /core/v1/sandbox/enroll [post]
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
	if decodeInputObject(raw, &input, "node_id", "credential", "name", "provider", "backend_fingerprint", "max_active", "max_retained") != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
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
// @Description Core deployment extension. Does not grant project resource access. Responses contain only explicit safe fields.
// @Tags Sandbox Manager
// @Produce json
// @Security NodeAuth
// @Param node_id query string true "Sandbox node UUID"
// @Success 200 {object} store.RuntimeNodeStatus
// @Failure 400,401,404,409,500,503 {object} v1.ErrorResponse
// @Router /core/v1/sandbox/node/identity [get]
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
