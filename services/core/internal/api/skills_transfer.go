package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/go-chi/chi/v5"
)

// @Summary Upload a Skill
// @Description Accepts one ZIP in files or a directory in files[]. Applies the qualified portable Skill bundle profile. No Beta header is required; full hosted upload limits and activation extensions are not qualified.
// @Tags Skills
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param files formData file true "Skill ZIP or directory files"
// @Success 200 {object} v1.Skill
// @Router /skills [post]
func (h *Handler) createSkill(w http.ResponseWriter, r *http.Request) { h.uploadSkill(w, r, false) }

// @Summary Upload an immutable Skill version
// @Tags Skills
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param skill_id path string true "Skill ID"
// @Param files formData file true "Skill ZIP or directory files"
// @Param default formData boolean false "Set as default"
// @Success 200 {object} v1.SkillVersion
// @Router /skills/{skill_id}/versions [post]
func (h *Handler) createSkillVersion(w http.ResponseWriter, r *http.Request) {
	h.uploadSkill(w, r, true)
}

func (h *Handler) uploadSkill(w http.ResponseWriter, r *http.Request, version bool) {
	if !h.skillsReady(w) {
		return
	}
	deadline := time.Now().Add(sourceTransferTimeout)
	controller := http.NewResponseController(w)
	if controller.SetReadDeadline(deadline) != nil || controller.SetWriteDeadline(deadline) != nil {
		writeError(w, http.StatusServiceUnavailable, "file_transfer_unavailable", "Bounded file transfer is unavailable.")
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	r.Body = http.MaxBytesReader(w, r.Body, agentskill.MaxExpandedBytes+(1<<20))
	archive, makeDefault, err := readSkillUpload(r, version)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeStoreError(w, r, store.ErrSourceFileTooLarge)
		} else {
			writeStoreError(w, r, store.ErrInvalidInput)
		}
		return
	}
	if version {
		result, err := h.skills.CreateSkillVersion(ctx, tenantID(r), chi.URLParam(r, "skill_id"), archive, makeDefault)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, skillVersionResponse(result))
	} else {
		result, err := h.skills.CreateSkill(ctx, tenantID(r), archive)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, skillResponseResource(result))
	}
}

// @Summary Download Skill content
// @Description Downloads an authorized ZIP using the default pointer when no concrete version is supplied. Exact upstream unversioned selection, content headers and range semantics remain unverified.
// @Tags Skills
// @Produce octet-stream
// @Security BearerAuth
// @Param skill_id path string true "Skill ID"
// @Success 200 {file} binary
// @Router /skills/{skill_id}/content [get]
func (h *Handler) skillContent(w http.ResponseWriter, r *http.Request) {
	if !h.skillsReady(w) {
		return
	}
	serveStoredContent(w, r, func(ctx context.Context, consume func(string, int64, io.Reader) error) error {
		var value store.SkillVersion
		var body []byte
		var err error
		if version := chi.URLParam(r, "version"); version != "" {
			value, body, err = h.skills.ReadSkillVersion(ctx, tenantID(r), chi.URLParam(r, "skill_id"), version)
		} else {
			value, body, err = h.skills.ReadDefaultSkillVersion(ctx, tenantID(r), chi.URLParam(r, "skill_id"))
		}
		if err != nil {
			return err
		}
		return consume(value.Name+".zip", int64(len(body)), bytes.NewReader(body))
	})
}

// @Summary Download immutable Skill version content
// @Tags Skills
// @Produce octet-stream
// @Security BearerAuth
// @Param skill_id path string true "Skill ID"
// @Param version path string true "Concrete version number"
// @Success 200 {file} binary
// @Router /skills/{skill_id}/versions/{version}/content [get]
func (h *Handler) skillVersionContent(w http.ResponseWriter, r *http.Request) {
	h.skillContent(w, r)
}
