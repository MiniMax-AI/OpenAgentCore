package api

import (
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/go-chi/chi/v5"
)

// @Summary List Skills
// @Description Lists tenant-owned metadata in timestamp order. Default page size 20, maximum 100. Limit 0 returns an empty page whose has_more reports whether any Skill follows the cursor; exact hosted defaults remain unverified.
// @Tags Skills
// @Produce json
// @Security BearerAuth
// @Param after query string false "Skill resource cursor"
// @Param limit query integer false "Page size; 0 returns an empty page" default(20) minimum(0) maximum(100)
// @Param order query string false "Creation order; omit for descending, explicit empty values are invalid" Enums(asc,desc)
// @Success 200 {object} v1.SkillList
// @Router /skills [get]
func (h *Handler) listSkills(w http.ResponseWriter, r *http.Request) {
	options, ok := readPage(w, r)
	if !ok {
		return
	}
	page, err := h.Skills.ListSkills(r.Context(), skills.ListSkills{TenantID: tenantID(r), After: options.after, Limit: options.limit, Ascending: options.ascending})
	if err != nil {
		writeSkillsError(w, r, err)
		return
	}
	result := v1.SkillList{Object: "list", Data: make([]v1.Skill, 0, len(page.Skills)), HasMore: page.HasMore}
	for _, value := range page.Skills {
		result.Data = append(result.Data, skillResponseResource(value))
	}
	if len(result.Data) > 0 {
		result.FirstID = &result.Data[0].ID
		result.LastID = &result.Data[len(result.Data)-1].ID
	}
	writeJSON(w, http.StatusOK, result)
}

// @Summary List Skill versions
// @Description Orders by version number; after identifies a version resource, not a version number. An after value that does not begin with skillver, or a version of another Skill, returns 400 invalid_value with param after; a missing version returns not found. No contents are decrypted. Limit 0 returns an empty page whose has_more reports whether any version follows the cursor.
// @Tags Skills
// @Produce json
// @Security BearerAuth
// @Param skill_id path string true "Skill ID"
// @Param after query string false "Version resource cursor"
// @Param limit query integer false "Page size; 0 returns an empty page" default(20) minimum(0) maximum(100)
// @Param order query string false "Version order; omit for descending, explicit empty values are invalid" Enums(asc,desc)
// @Success 200 {object} v1.SkillVersionList
// @Router /skills/{skill_id}/versions [get]
func (h *Handler) listSkillVersions(w http.ResponseWriter, r *http.Request) {
	options, ok := readPage(w, r)
	if !ok {
		return
	}
	page, err := h.Skills.ListVersions(r.Context(), skills.ListVersions{TenantID: tenantID(r), SkillID: skills.PathID(chi.URLParam(r, "skill_id")), After: options.after, Limit: options.limit, Ascending: options.ascending})
	if err != nil {
		writeSkillsError(w, r, err)
		return
	}
	result := v1.SkillVersionList{Object: "list", Data: make([]v1.SkillVersion, 0, len(page.Versions)), HasMore: page.HasMore}
	for _, value := range page.Versions {
		result.Data = append(result.Data, skillVersionResponse(value))
	}
	if len(result.Data) > 0 {
		result.FirstID = &result.Data[0].ID
		result.LastID = &result.Data[len(result.Data)-1].ID
	}
	writeJSON(w, http.StatusOK, result)
}
