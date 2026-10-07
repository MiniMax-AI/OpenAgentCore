package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/go-chi/chi/v5"
)

func (h *Handler) createSkill(w http.ResponseWriter, r *http.Request) { h.uploadSkill(w, r, false) }

func (h *Handler) createSkillVersion(w http.ResponseWriter, r *http.Request) {
	h.uploadSkill(w, r, true)
}

func (h *Handler) uploadSkill(w http.ResponseWriter, r *http.Request, version bool) {
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
			writeContentTooLarge(w)
		} else {
			writeSkillsError(w, r, skills.ErrInvalidInput)
		}
		return
	}
	if version {
		result, err := h.Skills.CreateVersion(ctx, skills.CreateVersion{TenantID: tenantID(r), SkillID: skills.PathID(chi.URLParam(r, "skill_id")), Archive: archive, MakeDefault: makeDefault})
		if err != nil {
			writeSkillsError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, skillVersionResponse(result))
	} else {
		result, err := h.Skills.CreateSkill(ctx, skills.CreateSkill{TenantID: tenantID(r), Archive: archive})
		if err != nil {
			writeSkillsError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, skillResponseResource(result))
	}
}

func (h *Handler) skillContent(w http.ResponseWriter, r *http.Request) {
	err := serveStoredContent(w, r, func(ctx context.Context, consume func(string, int64, io.Reader) error) error {
		skill := skills.PathID(chi.URLParam(r, "skill_id"))
		var content skills.Content
		var err error
		if version := chi.URLParam(r, "version"); version != "" {
			content, err = h.Skills.ReadVersion(ctx, skills.ReadVersion{TenantID: tenantID(r), SkillID: skill, Version: skills.PathVersion(version)})
		} else {
			content, err = h.Skills.ReadDefaultVersion(ctx, skills.ReadDefaultVersion{TenantID: tenantID(r), SkillID: skill})
		}
		if err != nil {
			return err
		}
		return consume(content.Version.Name+".zip", int64(len(content.Archive)), bytes.NewReader(content.Archive))
	})
	if err != nil {
		writeSkillsError(w, r, err)
	}
}

func (h *Handler) skillVersionContent(w http.ResponseWriter, r *http.Request) {
	h.skillContent(w, r)
}
