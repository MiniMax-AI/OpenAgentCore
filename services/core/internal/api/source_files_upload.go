package api

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
)

const sourceTransferTimeout = 5 * time.Minute

// @Summary Upload a source file
// @Description Accepts one multipart file and purpose=user_data in either order, with a private 512 MiB content limit and 64 KiB envelope allowance. Commits only after the entire request validates. The source is project-owned, independent of Sessions and workspace copies. No Beta header is required. Other purposes, expires_after, listing, resumable Uploads, quotas/rate-limit and complete hosted error/status parity remain unsupported or unverified.
// @Tags Files
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param file formData file true "Source bytes"
// @Param purpose formData string true "user_data" Enums(user_data)
// @Success 200 {object} v1.SourceFile
// @Failure 400,401,413,500,503 {object} v1.ErrorResponse
// @Router /files [post]
func (h *Handler) createSourceFile(w http.ResponseWriter, r *http.Request) {
	deadline := time.Now().Add(sourceTransferTimeout)
	controller := http.NewResponseController(w)
	if controller.SetReadDeadline(deadline) != nil || controller.SetWriteDeadline(deadline) != nil {
		writeError(w, http.StatusServiceUnavailable, "file_transfer_unavailable", "Bounded file transfer is unavailable.")
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	r.Body = http.MaxBytesReader(w, r.Body, files.MaxBytes+(64<<10))
	file, err := h.Files.Create(ctx, files.CreateCommand{TenantID: tenantID(r), Upload: func(dst io.Writer) (files.Upload, error) {
		return readSourceUpload(r, dst)
	}})
	if err != nil {
		writeFilesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sourceFileResponse(file))
}

func readSourceUpload(r *http.Request, dst io.Writer) (files.Upload, error) {
	var input files.Upload
	if r.Header.Get("Content-Encoding") != "" {
		return input, files.ErrInvalidInput
	}
	multi, err := r.MultipartReader()
	if err != nil {
		return input, files.ErrInvalidInput
	}
	seen := make(map[string]bool, 2)
	buffer := make([]byte, 256<<10)
	for {
		part, err := multi.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return input, sourceUploadError(err)
		}
		kind, attrs, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		name := attrs["name"]
		if err != nil || kind != "form-data" || seen[name] || (name != "file" && name != "purpose") || part.Header.Get("Content-Transfer-Encoding") != "" {
			return input, files.ErrInvalidInput
		}
		seen[name] = true
		if name == "file" {
			input.Filename = attrs["filename"]
			if _, err := io.CopyBuffer(dst, part, buffer); err != nil {
				return input, sourceUploadError(err)
			}
		} else {
			if _, exists := attrs["filename"]; exists {
				return input, files.ErrInvalidInput
			}
			value, err := io.ReadAll(io.LimitReader(part, 65))
			if err != nil || len(value) > 64 {
				return input, files.ErrInvalidInput
			}
			input.Purpose = string(value)
		}
		if err := part.Close(); err != nil {
			return input, sourceUploadError(err)
		}
	}
	if _, err := io.CopyBuffer(io.Discard, r.Body, buffer); err != nil {
		return input, sourceUploadError(err)
	}
	if !seen["file"] || !seen["purpose"] || input.Purpose != files.PurposeUserData {
		return input, files.ErrInvalidInput
	}
	return input, nil
}

func sourceUploadError(err error) error {
	var limit *http.MaxBytesError
	if errors.As(err, &limit) || errors.Is(err, files.ErrTooLarge) {
		return files.ErrTooLarge
	}
	return files.ErrInvalidInput
}
