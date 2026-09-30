package api

import (
	"context"
	"io"
	"net/http"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
	"github.com/go-chi/chi/v5"
)

// Files creates and deletes project-owned source Files.
type Files interface {
	Create(context.Context, files.CreateCommand) (files.File, error)
	Delete(context.Context, files.DeleteCommand) error
}

// FilesReader reads source File metadata and streams their bytes.
type FilesReader interface {
	Get(ctx context.Context, tenantID, fileID string) (files.File, error)
	List(ctx context.Context, tenantID string, query files.ListQuery) (files.Page, error)
	Read(ctx context.Context, tenantID, fileID string, consume func(files.File, io.Reader) error) error
}

// @Summary Retrieve source file metadata
// @Description Returns immutable project-owned user_data file metadata. No Beta header is required. Other purposes, expiration and full hosted status/error semantics remain unimplemented or unverified.
// @Tags Files
// @Produce json
// @Security BearerAuth
// @Param file_id path string true "Source file ID"
// @Success 200 {object} v1.SourceFile
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /files/{file_id} [get]
func (h *Handler) getSourceFile(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	file, err := h.FilesReader.Get(ctx, tenantID(r), chi.URLParam(r, "file_id"))
	if err != nil {
		writeFilesError(w, r, err, "id")
		return
	}
	writeJSON(w, http.StatusOK, sourceFileResponse(file))
}

// @Summary Delete a source file
// @Description Atomically deletes project-owned metadata and stored bytes. Already-admitted reads or copies may finish. Workspace copies remain independent. Historical WAL/backups are not erased. No Beta header is required; exact hosted concurrent deletion/error semantics remain unverified.
// @Tags Files
// @Produce json
// @Security BearerAuth
// @Param file_id path string true "Source file ID"
// @Success 200 {object} v1.SourceFileDeleted
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /files/{file_id} [delete]
func (h *Handler) deleteSourceFile(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	id := chi.URLParam(r, "file_id")
	if err := h.Files.Delete(ctx, files.DeleteCommand{TenantID: tenantID(r), FileID: id}); err != nil {
		writeFilesError(w, r, err, "id")
		return
	}
	writeJSON(w, http.StatusOK, v1.SourceFileDeleted{ID: id, Object: "file", Deleted: true})
}

func sourceFileResponse(file files.File) v1.SourceFile {
	return v1.SourceFile{ID: file.ID, Object: "file", Bytes: file.SizeBytes,
		CreatedAt: file.CreatedAt.Unix(), Filename: file.Filename,
		Purpose: file.Purpose, Status: "processed"}
}
