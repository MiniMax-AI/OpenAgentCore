package api

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// @Summary Download source file bytes
// @Description Resolves project-owned File metadata before enforcing download policy. Public download of user_data Files returns 400; missing and foreign Files return the same 404. Internal initial-file and workspace copies remain available. No Beta header is required.
// @Tags Files
// @Produce json
// @Security BearerAuth
// @Param file_id path string true "Source file ID"
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /files/{file_id}/content [get]
func (h *Handler) sourceFileContent(w http.ResponseWriter, r *http.Request) {
	if !h.sourceFilesAvailable(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	_, err := h.sourceFiles.GetSourceFile(ctx, tenantID(r), chi.URLParam(r, "file_id"))
	if err != nil {
		writeStoreError(w, r, err, "id")
		return
	}
	writeError(w, http.StatusBadRequest, "", "Not allowed to download files of purpose: user_data")
}

func serveStoredContent(w http.ResponseWriter, r *http.Request, read func(context.Context, func(string, int64, io.Reader) error) error, notFoundParam ...string) {
	deadline := time.Now().Add(sourceTransferTimeout)
	if http.NewResponseController(w).SetWriteDeadline(deadline) != nil {
		writeError(w, http.StatusServiceUnavailable, "file_transfer_unavailable", "Bounded file transfer is unavailable.")
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	started := false
	err := read(ctx, func(filename string, size int64, body io.Reader) error {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		started = true
		w.WriteHeader(http.StatusOK)
		n, err := io.CopyBuffer(w, body, make([]byte, 256<<10))
		if err == nil && n != size {
			err = io.ErrUnexpectedEOF
		}
		return err
	})
	if err != nil {
		if started {
			panic(http.ErrAbortHandler)
		}
		writeStoreError(w, r, err, notFoundParam...)
	}
}
