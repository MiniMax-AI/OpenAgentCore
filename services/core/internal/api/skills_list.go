package api

import (
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/go-chi/chi/v5"
)

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
