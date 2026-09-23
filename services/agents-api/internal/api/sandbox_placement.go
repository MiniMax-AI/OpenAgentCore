package api

import (
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

type SandboxNodeDirectoryEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
}
type SandboxNodeDirectory struct {
	Data []SandboxNodeDirectoryEntry `json:"data"`
}
type SandboxSessionPlacement = store.RuntimePlacement

// Project nodes expose only selector labels and availability, never other tenants' allocations.
func (h *Handler) registerSandboxProjectRoutes(r chi.Router) {
	if h.sandboxStore == nil {
		return
	}
	r.Get("/sandbox/nodes", h.sandboxNodeDirectory)
	r.Get("/agents/sessions/{session_id}/sandbox-placement", h.sandboxSessionPlacement)
}

// @Summary List hosted sandbox node selectors
// @Description Core extension. Returns only selector identity, label and admission availability for project clients.
// @Tags Core
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Success 200 {object} api.SandboxNodeDirectory
// @Failure 401,500 {object} v1.ErrorResponse
// @Router /sandbox/nodes [get]
func (h *Handler) sandboxNodeDirectory(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.sandboxStore.ListRuntimeNodes(r.Context())
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	deployment, err := h.sandboxStore.GetRuntimeDeployment(r.Context())
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	result := make([]SandboxNodeDirectoryEntry, 0, len(nodes))
	for _, n := range nodes {
		result = append(result, SandboxNodeDirectoryEntry{ID: n.ID, Name: n.Name, Available: n.Online && n.ProviderReady && !deployment.Maintenance && n.Active < int64(n.MaxActive) && n.Retained < int64(n.MaxRetained)})
	}
	writeJSON(w, http.StatusOK, SandboxNodeDirectory{Data: result})
}

// @Summary Retrieve a Session sandbox placement
// @Description Core extension. Reads the caller-owned Session's fixed sandbox node, independently of node connectivity.
// @Tags Core
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Success 200 {object} api.SandboxSessionPlacement
// @Failure 401,404,500 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/sandbox-placement [get]
func (h *Handler) sandboxSessionPlacement(w http.ResponseWriter, r *http.Request) {
	result, err := h.sandboxStore.GetSessionRuntimePlacement(r.Context(), tenantID(r), chi.URLParam(r, "session_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
