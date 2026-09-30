package api

import (
	"context"
	"net/http"
	"strconv"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Skills runs the Skill use cases: uploads, the default pointer, deletion,
// lists and content reads.
type Skills interface {
	CreateSkill(context.Context, skills.CreateSkill) (skills.Skill, error)
	CreateVersion(context.Context, skills.CreateVersion) (skills.Version, error)
	SetDefaultVersion(context.Context, skills.SetDefaultVersion) (skills.Skill, error)
	DeleteSkill(context.Context, skills.DeleteSkill) error
	DeleteVersion(context.Context, skills.DeleteVersion) (skills.Version, error)
	ListSkills(context.Context, skills.ListSkills) (skills.Page, error)
	ListVersions(context.Context, skills.ListVersions) (skills.VersionPage, error)
	ReadVersion(context.Context, skills.ReadVersion) (skills.Content, error)
	ReadDefaultVersion(context.Context, skills.ReadDefaultVersion) (skills.Content, error)
}

// SkillsReader reads Skill and version metadata.
type SkillsReader interface {
	Skill(ctx context.Context, tenantID string, id uuid.UUID) (skills.Skill, error)
	Version(ctx context.Context, tenantID string, skillID uuid.UUID, version int64) (skills.Version, error)
}

func (h *Handler) registerSkillRoutes(r chi.Router) {
	r.Post("/v1/skills", h.createSkill)
	r.Get("/v1/skills", h.listSkills)
	r.Get("/v1/skills/{skill_id}", h.getSkill)
	r.Post("/v1/skills/{skill_id}", h.updateSkill)
	r.Delete("/v1/skills/{skill_id}", h.deleteSkill)
	r.Get("/v1/skills/{skill_id}/content", h.skillContent)
	r.Head("/v1/skills/{skill_id}/content", methodNotAllowed)
	r.Post("/v1/skills/{skill_id}/versions", h.createSkillVersion)
	r.Get("/v1/skills/{skill_id}/versions", h.listSkillVersions)
	r.Get("/v1/skills/{skill_id}/versions/{version}", h.getSkillVersion)
	r.Delete("/v1/skills/{skill_id}/versions/{version}", h.deleteSkillVersion)
	r.Get("/v1/skills/{skill_id}/versions/{version}/content", h.skillVersionContent)
	r.Head("/v1/skills/{skill_id}/versions/{version}/content", methodNotAllowed)
}

// @Summary Retrieve Skill metadata
// @Description Returns tenant-owned metadata without decrypting contents or starting Runtime. No Beta header is required.
// @Tags Skills
// @Produce json
// @Security BearerAuth
// @Param skill_id path string true "Skill ID"
// @Success 200 {object} v1.Skill
// @Router /skills/{skill_id} [get]
func (h *Handler) getSkill(w http.ResponseWriter, r *http.Request) {
	value, err := h.SkillsReader.Skill(r.Context(), tenantID(r), skills.PathID(chi.URLParam(r, "skill_id")))
	if err != nil {
		writeSkillsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, skillResponseResource(value))
}

// @Summary Update the default Skill version
// @Description Changes only the tenant-owned default pointer; immutable versions and existing Session snapshots remain unchanged.
// @Tags Skills
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param skill_id path string true "Skill ID"
// @Param body body v1.SkillUpdateRequest true "Default version"
// @Success 200 {object} v1.Skill
// @Router /skills/{skill_id} [post]
func (h *Handler) updateSkill(w http.ResponseWriter, r *http.Request) {
	body, ok := readJSONBodyLimit(w, r, 64<<10, "Request exceeds 64 KiB.")
	if !ok {
		return
	}
	var input v1.SkillUpdateRequest
	if decodeInputObject(body, &input, "default_version") != nil || input.DefaultVersion == "" {
		writeSkillsError(w, r, skills.ErrInvalidInput)
		return
	}
	value, err := h.Skills.SetDefaultVersion(r.Context(), skills.SetDefaultVersion{TenantID: tenantID(r), SkillID: skills.PathID(chi.URLParam(r, "skill_id")), Version: input.DefaultVersion})
	if err != nil {
		writeSkillsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, skillResponseResource(value))
}

// @Summary Delete a Skill and its versions
// @Description Deletes tenant-owned source bundles. Existing Session installation snapshots remain independent.
// @Tags Skills
// @Produce json
// @Security BearerAuth
// @Param skill_id path string true "Skill ID"
// @Success 200 {object} v1.SkillDeleted
// @Router /skills/{skill_id} [delete]
func (h *Handler) deleteSkill(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "skill_id")
	if err := h.Skills.DeleteSkill(r.Context(), skills.DeleteSkill{TenantID: tenantID(r), SkillID: skills.PathID(id)}); err != nil {
		writeSkillsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.SkillDeleted{ID: id, Object: "skill.deleted", Deleted: true})
}

// @Summary Retrieve Skill version metadata
// @Tags Skills
// @Produce json
// @Security BearerAuth
// @Param skill_id path string true "Skill ID"
// @Param version path string true "Concrete version number"
// @Success 200 {object} v1.SkillVersion
// @Router /skills/{skill_id}/versions/{version} [get]
func (h *Handler) getSkillVersion(w http.ResponseWriter, r *http.Request) {
	value, err := h.SkillsReader.Version(r.Context(), tenantID(r), skills.PathID(chi.URLParam(r, "skill_id")), skills.PathVersion(chi.URLParam(r, "version")))
	if err != nil {
		writeSkillsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, skillVersionResponse(value))
}

// @Summary Delete a Skill version
// @Description Deleting the only remaining version also deletes the Skill; existing Session installation snapshots remain independent. The default version cannot be deleted while other versions remain. Version numbers are never reused.
// @Tags Skills
// @Produce json
// @Security BearerAuth
// @Param skill_id path string true "Skill ID"
// @Param version path string true "Concrete version number"
// @Success 200 {object} v1.SkillVersionDeleted
// @Router /skills/{skill_id}/versions/{version} [delete]
func (h *Handler) deleteSkillVersion(w http.ResponseWriter, r *http.Request) {
	value, err := h.Skills.DeleteVersion(r.Context(), skills.DeleteVersion{TenantID: tenantID(r), SkillID: skills.PathID(chi.URLParam(r, "skill_id")), Version: skills.PathVersion(chi.URLParam(r, "version"))})
	if err != nil {
		writeSkillsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.SkillVersionDeleted{ID: value.ID, Object: "skill.version.deleted", Version: strconv.FormatInt(value.Version, 10), Deleted: true})
}

func skillResponseResource(s skills.Skill) v1.Skill {
	return v1.Skill{ID: s.ID, Object: "skill", CreatedAt: s.CreatedAt.Unix(), Name: s.Name, Description: s.Description, DefaultVersion: strconv.FormatInt(s.DefaultVersion, 10), LatestVersion: strconv.FormatInt(s.LatestVersion, 10)}
}
func skillVersionResponse(s skills.Version) v1.SkillVersion {
	return v1.SkillVersion{ID: s.ID, Object: "skill.version", SkillID: s.SkillID, CreatedAt: s.CreatedAt.Unix(), Name: s.Name, Description: s.Description, Version: strconv.FormatInt(s.Version, 10)}
}
