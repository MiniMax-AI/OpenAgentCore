package api

import (
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/go-chi/chi/v5"
)

// @Summary List Skills
// @Description Lists tenant-owned metadata in timestamp order. Default page size 20, maximum 100; exact hosted defaults and limit-zero semantics remain unverified.
// @Tags Skills
// @Produce json
// @Security BearerAuth
// @Param after query string false "Skill resource cursor"
// @Param limit query integer false "Page size"
// @Param order query string false "Creation order; omit for descending, explicit empty values are invalid" Enums(asc,desc)
// @Success 200 {object} v1.SkillList
// @Router /skills [get]
func (h *Handler) listSkills(w http.ResponseWriter, r *http.Request) {
	if !h.skillsReady(w, r, true) {
		return
	}
	options, ok := readPage(w, r)
	if !ok {
		return
	}
	page, err := h.skills.ListSkills(r.Context(), tenantID(r), options.after, options.limit, options.ascending)
	if err != nil {
		writeStoreError(w, r, err)
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
// @Description Orders by version number; after identifies a version resource, not a version number. No contents are decrypted.
// @Tags Skills
// @Produce json
// @Security BearerAuth
// @Param skill_id path string true "Skill ID"
// @Param after query string false "Version resource cursor"
// @Param limit query integer false "Page size"
// @Param order query string false "Version order; omit for descending, explicit empty values are invalid" Enums(asc,desc)
// @Success 200 {object} v1.SkillVersionList
// @Router /skills/{skill_id}/versions [get]
func (h *Handler) listSkillVersions(w http.ResponseWriter, r *http.Request) {
	if !h.skillsReady(w, r, true) {
		return
	}
	options, ok := readPage(w, r)
	if !ok {
		return
	}
	page, err := h.skills.ListSkillVersions(r.Context(), tenantID(r), chi.URLParam(r, "skill_id"), options.after, options.limit, options.ascending)
	if err != nil {
		writeStoreError(w, r, err)
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
