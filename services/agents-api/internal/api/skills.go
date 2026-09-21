package api

import (
	"context"
	"net/http"
	"strconv"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

type SkillStore interface {
	CreateSkill(context.Context, string, []byte) (store.Skill, error)
	GetSkill(context.Context, string, string) (store.Skill, error)
	UpdateSkillDefault(context.Context, string, string, string) (store.Skill, error)
	DeleteSkill(context.Context, string, string) error
	ListSkills(context.Context, string, string, int, bool) (store.SkillPage, error)
	CreateSkillVersion(context.Context, string, string, []byte, bool) (store.SkillVersion, error)
	GetSkillVersion(context.Context, string, string, string) (store.SkillVersion, error)
	ReadSkillVersion(context.Context, string, string, string) (store.SkillVersion, []byte, error)
	ReadDefaultSkillVersion(context.Context, string, string) (store.SkillVersion, []byte, error)
	DeleteSkillVersion(context.Context, string, string, string) (store.SkillVersion, error)
	ListSkillVersions(context.Context, string, string, string, int, bool) (store.SkillVersionPage, error)
}

func WithSkills(s SkillStore) Option { return func(h *Handler) { h.skills = s } }

func (h *Handler) registerSkillRoutes(r chi.Router) {
	r.Post("/v1/skills", h.createSkill)
	r.Get("/v1/skills", h.listSkills)
	r.Get("/v1/skills/{skill_id}", h.getSkill)
	r.Post("/v1/skills/{skill_id}", h.updateSkill)
	r.Delete("/v1/skills/{skill_id}", h.deleteSkill)
	r.Get("/v1/skills/{skill_id}/content", h.skillContent)
	r.Post("/v1/skills/{skill_id}/versions", h.createSkillVersion)
	r.Get("/v1/skills/{skill_id}/versions", h.listSkillVersions)
	r.Get("/v1/skills/{skill_id}/versions/{version}", h.getSkillVersion)
	r.Delete("/v1/skills/{skill_id}/versions/{version}", h.deleteSkillVersion)
	r.Get("/v1/skills/{skill_id}/versions/{version}/content", h.skillVersionContent)
}

func (h *Handler) skillsReady(w http.ResponseWriter, r *http.Request, list bool) bool {
	if h.skills == nil {
		writeError(w, http.StatusServiceUnavailable, "skill_storage_unavailable", "Skill storage is unavailable.")
		return false
	}
	if !list && r.URL.RawQuery != "" {
		writeStoreError(w, r, store.ErrInvalidInput)
		return false
	}
	return true
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
	if !h.skillsReady(w, r, false) {
		return
	}
	value, err := h.skills.GetSkill(r.Context(), tenantID(r), chi.URLParam(r, "skill_id"))
	if err != nil {
		writeStoreError(w, r, err)
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
	if !h.skillsReady(w, r, false) {
		return
	}
	body, ok := readJSONBodyLimit(w, r, 64<<10, "Request exceeds 64 KiB.")
	if !ok {
		return
	}
	var input v1.SkillUpdateRequest
	if decodeInputObject(body, &input, "default_version") != nil || input.DefaultVersion == "" {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	value, err := h.skills.UpdateSkillDefault(r.Context(), tenantID(r), chi.URLParam(r, "skill_id"), input.DefaultVersion)
	if err != nil {
		writeStoreError(w, r, err)
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
	if !h.skillsReady(w, r, false) {
		return
	}
	id := chi.URLParam(r, "skill_id")
	if err := h.skills.DeleteSkill(r.Context(), tenantID(r), id); err != nil {
		writeStoreError(w, r, err)
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
	if !h.skillsReady(w, r, false) {
		return
	}
	value, err := h.skills.GetSkillVersion(r.Context(), tenantID(r), chi.URLParam(r, "skill_id"), chi.URLParam(r, "version"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, skillVersionResponse(value))
}

// @Summary Delete a Skill version
// @Description Rejects deletion of the current default version. Exact hosted last-version/default deletion precedence is not verified.
// @Tags Skills
// @Produce json
// @Security BearerAuth
// @Param skill_id path string true "Skill ID"
// @Param version path string true "Concrete version number"
// @Success 200 {object} v1.SkillVersionDeleted
// @Router /skills/{skill_id}/versions/{version} [delete]
func (h *Handler) deleteSkillVersion(w http.ResponseWriter, r *http.Request) {
	if !h.skillsReady(w, r, false) {
		return
	}
	value, err := h.skills.DeleteSkillVersion(r.Context(), tenantID(r), chi.URLParam(r, "skill_id"), chi.URLParam(r, "version"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.SkillVersionDeleted{ID: value.ID, Object: "skill.version.deleted", Version: strconv.FormatInt(value.Version, 10), Deleted: true})
}

func skillResponseResource(s store.Skill) v1.Skill {
	return v1.Skill{ID: s.ID, Object: "skill", CreatedAt: s.CreatedAt.Unix(), Name: s.Name, Description: s.Description, DefaultVersion: strconv.FormatInt(s.DefaultVersion, 10), LatestVersion: strconv.FormatInt(s.LatestVersion, 10)}
}
func skillVersionResponse(s store.SkillVersion) v1.SkillVersion {
	return v1.SkillVersion{ID: s.ID, Object: "skill.version", SkillID: s.SkillID, CreatedAt: s.CreatedAt.Unix(), Name: s.Name, Description: s.Description, Version: strconv.FormatInt(s.Version, 10)}
}
