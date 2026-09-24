package api

import "net/http"

// @Summary List Skills in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Skills
// @Produce json
// @Security DeploymentAdminAuth
// @Param after query string false "Skill resource cursor"
// @Param limit query integer false "Page size; 0 returns an empty page" default(20) minimum(0) maximum(100)
// @Param order query string false "Creation order; omit for descending, explicit empty values are invalid" Enums(asc,desc)
// @Success 200 {object} v1.SkillList
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/skills [get]
func (h *Handler) adminListSkills(w http.ResponseWriter, r *http.Request) {
	h.listSkills(w, r)
}

// @Summary Retrieve Skill metadata in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Skills
// @Produce json
// @Security DeploymentAdminAuth
// @Param skill_id path string true "Skill ID"
// @Success 200 {object} v1.Skill
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/skills/{skill_id} [get]
func (h *Handler) adminGetSkill(w http.ResponseWriter, r *http.Request) {
	h.getSkill(w, r)
}

// @Summary Delete a Skill and its versions in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Skills
// @Produce json
// @Security DeploymentAdminAuth
// @Param skill_id path string true "Skill ID"
// @Success 200 {object} v1.SkillDeleted
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/skills/{skill_id} [delete]
func (h *Handler) adminDeleteSkill(w http.ResponseWriter, r *http.Request) {
	h.deleteSkill(w, r)
}

// @Summary Download Skill content in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Skills
// @Produce octet-stream
// @Security DeploymentAdminAuth
// @Param skill_id path string true "Skill ID"
// @Success 200 {file} binary
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/skills/{skill_id}/content [get]
func (h *Handler) adminSkillContent(w http.ResponseWriter, r *http.Request) {
	h.skillContent(w, r)
}

// @Summary List Skill versions in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Skills
// @Produce json
// @Security DeploymentAdminAuth
// @Param skill_id path string true "Skill ID"
// @Param after query string false "Version resource cursor"
// @Param limit query integer false "Page size; 0 returns an empty page" default(20) minimum(0) maximum(100)
// @Param order query string false "Version order; omit for descending, explicit empty values are invalid" Enums(asc,desc)
// @Success 200 {object} v1.SkillVersionList
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/skills/{skill_id}/versions [get]
func (h *Handler) adminListSkillVersions(w http.ResponseWriter, r *http.Request) {
	h.listSkillVersions(w, r)
}

// @Summary Retrieve Skill version metadata in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Skills
// @Produce json
// @Security DeploymentAdminAuth
// @Param skill_id path string true "Skill ID"
// @Param version path string true "Concrete version number"
// @Success 200 {object} v1.SkillVersion
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/skills/{skill_id}/versions/{version} [get]
func (h *Handler) adminGetSkillVersion(w http.ResponseWriter, r *http.Request) {
	h.getSkillVersion(w, r)
}

// @Summary Delete a Skill version in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Skills
// @Produce json
// @Security DeploymentAdminAuth
// @Param skill_id path string true "Skill ID"
// @Param version path string true "Concrete version number"
// @Success 200 {object} v1.SkillVersionDeleted
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/skills/{skill_id}/versions/{version} [delete]
func (h *Handler) adminDeleteSkillVersion(w http.ResponseWriter, r *http.Request) {
	h.deleteSkillVersion(w, r)
}

// @Summary Download immutable Skill version content in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Skills
// @Produce octet-stream
// @Security DeploymentAdminAuth
// @Param skill_id path string true "Skill ID"
// @Param version path string true "Concrete version number"
// @Success 200 {file} binary
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/skills/{skill_id}/versions/{version}/content [get]
func (h *Handler) adminSkillVersionContent(w http.ResponseWriter, r *http.Request) {
	h.skillVersionContent(w, r)
}
