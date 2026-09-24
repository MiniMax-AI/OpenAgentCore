package api

import "net/http"

// @Summary List reusable Agents in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Agents
// @Produce json
// @Security DeploymentAdminAuth
// @Param after query string false "Last Agent ID from the previous page"
// @Param limit query int64 false "Page size; 0 is treated as 1 and values above 100 as 100" minimum(0) default(20)
// @Param order query string false "Creation order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.SavedAgentList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/agents [get]
func (h *Handler) adminListAgents(w http.ResponseWriter, r *http.Request) {
	h.listAgents(w, r)
}

// @Summary Retrieve a reusable Agent in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Agents
// @Produce json
// @Security DeploymentAdminAuth
// @Param agent_id path string true "Agent ID"
// @Success 200 {object} v1.SavedAgent
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/agents/{agent_id} [get]
func (h *Handler) adminGetAgent(w http.ResponseWriter, r *http.Request) {
	h.getAgent(w, r)
}

// @Summary Delete a reusable Agent in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Agents
// @Produce json
// @Security DeploymentAdminAuth
// @Param agent_id path string true "Agent ID"
// @Success 200 {object} v1.AgentDeleted
// @Failure 400,401,404,413,500 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/agents/{agent_id} [delete]
func (h *Handler) adminDeleteAgent(w http.ResponseWriter, r *http.Request) {
	h.deleteAgent(w, r)
}
