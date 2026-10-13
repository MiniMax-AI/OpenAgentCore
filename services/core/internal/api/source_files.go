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
