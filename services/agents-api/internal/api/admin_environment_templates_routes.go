package api

import "net/http"

// @Summary List Environment Templates in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Environment Templates
// @Produce json
// @Security DeploymentAdminAuth
// @Param after query string false "Previous Template ID"
// @Param limit query integer false "Page size; 0 is treated as 1 and values above 100 as 100" default(20) minimum(0)
// @Param order query string false "Creation order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.EnvironmentTemplateList
// @Failure 400,401,404,500 {object} CoreErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/environment-templates [get]
func (h *Handler) adminListEnvironmentTemplates(w http.ResponseWriter, r *http.Request) {
	h.listEnvironmentTemplates(w, r)
}

// @Summary Retrieve an Environment Template in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Environment Templates
// @Produce json
// @Security DeploymentAdminAuth
// @Param environment_template_id path string true "Template ID"
// @Success 200 {object} v1.EnvironmentTemplate
// @Failure 400,401,404,500 {object} CoreErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/environment-templates/{environment_template_id} [get]
func (h *Handler) adminGetEnvironmentTemplate(w http.ResponseWriter, r *http.Request) {
	h.getEnvironmentTemplate(w, r)
}

// @Summary Delete an Environment Template in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Environment Templates
// @Produce json
// @Security DeploymentAdminAuth
// @Param environment_template_id path string true "Template ID"
// @Success 200 {object} v1.EnvironmentTemplateDeleted
// @Failure 400,401,404,500 {object} CoreErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/environment-templates/{environment_template_id} [delete]
func (h *Handler) adminDeleteEnvironmentTemplate(w http.ResponseWriter, r *http.Request) {
	h.deleteEnvironmentTemplate(w, r)
}
