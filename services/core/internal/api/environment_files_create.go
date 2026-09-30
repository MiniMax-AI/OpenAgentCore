package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/echotext"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/go-chi/chi/v5"
)

// @Summary Create an Environment file from inline bytes or a source file
// @Description Uploads standard Base64 bytes to a file beneath /workspace in a qualified local Environment and returns 201. Accepts inline bytes or a project-owned source file_id through the same write path. Unknown body fields are rejected with their name as param. Basic public hosted creation requires explicit managed Runtime configuration; an openai_hosted Environment that has not connected yet returns 400. Inline data is limited to 5 MiB decoded and a file_id copy to 50 MiB. Missing parent directories are created with mode 0700 and the file with mode 0600. An existing destination is never replaced; a directory, an existing file or a path through a symlink or non-directory returns 400. Idle writes exclude execution. Missing receipts return unavailable and retain a durable mutation gate without automatic replay. Error/timing parity with upstream remains unverified.
// @Tags Environments
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param environment_id path string true "Environment ID"
// @Param request body v1.EnvironmentFileCreateRequest true "Inline bytes or source file ID and absolute workspace path"
// @Success 201 {object} v1.EnvironmentFile
// @Failure 400,401,404,409,413,500,503 {object} v1.ErrorResponse
// @Router /agents/environments/{environment_id}/files [post]
func (h *Handler) createEnvironmentFile(w http.ResponseWriter, r *http.Request) {
	const maxJSON = int64(((proto.WorkspaceWriteMaxBytes+2)/3)*4 + (16 << 10))
	raw, ok := readJSONObjectLimit(w, r, maxJSON, "Inline upload exceeds this service's bounded file limit.")
	if !ok {
		return
	}
	environment, err := h.Environments.GetEnvironment(r.Context(), tenantID(r), chi.URLParam(r, "environment_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	var request v1.EnvironmentFileCreateRequest
	fields := []string{"type", "path", "data", "file_id"}
	if field, found := unknownBodyField(raw, fields...); found {
		if !echoableField(field) {
			writeFieldError(w, errUnknownEnvironmentFileField)
			return
		}
		writeFieldError(w, &fieldError{param: field, message: "Unknown parameter: '" + field + "'."})
		return
	}
	if err := decodeInputObject(raw, &request, fields...); err != nil || request.Path == nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	switch request.Type {
	case "inline":
		fields = []string{"type", "path", "data"}
		if request.Data == nil {
			writeStoreError(w, r, store.ErrInvalidInput)
			return
		}
	case "file_id":
		fields = []string{"type", "path", "file_id"}
		if request.FileID == nil || *request.FileID == "" {
			writeStoreError(w, r, store.ErrInvalidInput)
			return
		}
	default:
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	if decodeInputObject(raw, &request, fields...) != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	if err := environmentFileCreatePathError(*request.Path); err != nil {
		writeFieldError(w, err)
		return
	}
	var data []byte
	if request.Type == "inline" {
		data, err = base64.StdEncoding.Strict().DecodeString(*request.Data)
		if err != nil {
			writeStoreError(w, r, store.ErrInvalidInput)
			return
		}
		if len(data) > maxInlineEnvironmentFileBytes {
			writeFieldError(w, errEnvironmentFileInlineTooLarge)
			return
		}
	}
	if !environmentFilesAccessible(w, environment) {
		return
	}
	if request.Type == "file_id" {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		err = h.FilesReader.Read(ctx, tenantID(r), *request.FileID, func(file files.File, body io.Reader) error {
			if file.SizeBytes > proto.WorkspaceWriteMaxBytes {
				return files.ErrTooLarge
			}
			data, err = io.ReadAll(io.LimitReader(body, proto.WorkspaceWriteMaxBytes+1))
			if err == nil && int64(len(data)) != file.SizeBytes {
				return io.ErrUnexpectedEOF
			}
			return err
		})
		if err != nil {
			writeFilesError(w, r, err)
			return
		}
	}
	if h.Execution == nil || !execution.LocalWorkspaceConfiguration(environment.Configuration) {
		writeStoreError(w, r, execution.ErrExecutionUnavailable)
		return
	}
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(215 * time.Second)); err != nil {
		writeStoreError(w, r, execution.ErrExecutionUnavailable)
		return
	}
	size, err := h.Execution.Workspaces.WriteEnvironmentFile(r.Context(), environment, strings.TrimPrefix(*request.Path, "/workspace/"), data)
	if err != nil {
		if !writeFieldError(w, environmentFileWriteError(err)) {
			writeStoreError(w, r, err)
		}
		return
	}
	if size != int64(len(data)) {
		writeStoreError(w, r, execution.ErrExecutionUnavailable)
		return
	}
	writeJSON(w, http.StatusCreated, v1.EnvironmentFile{EnvironmentID: environment.ID, Object: "agent.environment.file", Path: *request.Path, SizeBytes: size})
}

// Official Files.create path errors (HE-16); the messages keep the observed field name.
var (
	errEnvironmentFileCreatePath       = &fieldError{message: "environment.files[0].path must be an absolute POSIX path inside /workspace"}
	errEnvironmentFileCreateComponents = &fieldError{message: "environment.files[0].path cannot contain empty, . or .. path components"}
)

// maxInlineEnvironmentFileBytes is the official decoded inline bound (HE-15),
// measured before any Runtime work. file_id copies keep the 50 MiB destination bound.
const maxInlineEnvironmentFileBytes = 5 << 20

// Official Files.create size and destination errors (HE-12, HE-13, HE-15).
var (
	errEnvironmentFileInlineTooLarge = &fieldError{message: "environment.files[0].data exceeds the 5 MiB decoded limit"}
	errEnvironmentFileConflict       = &fieldError{message: "file path conflicts with an existing environment file"}
	errEnvironmentFileUnsafe         = &fieldError{message: "environment.files paths must not traverse symlinks or overwrite existing files"}
)

// environmentFileWriteError maps the installer's known destination refusals.
// Core keeps no path ledger of earlier writes, so an existing regular file uses
// the untracked-file message even when an earlier Files.create wrote it.
func environmentFileWriteError(err error) error {
	switch {
	case errors.Is(err, execution.ErrEnvironmentFileDirectory):
		return errEnvironmentFileConflict
	case errors.Is(err, execution.ErrEnvironmentFileUnsafe):
		return errEnvironmentFileUnsafe
	}
	return nil
}

// environmentFileCreatePathError accepts exactly the canonical absolute paths
// below /workspace; the accepted set is unchanged, only the errors are specific.
func environmentFileCreatePathError(value string) error {
	if !strings.HasPrefix(value, "/") {
		return errEnvironmentFileCreatePath
	}
	for _, component := range strings.Split(value[1:], "/") {
		if component == "" || component == "." || component == ".." {
			return errEnvironmentFileCreateComponents
		}
	}
	if len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\\x00\r\n") || !strings.HasPrefix(value, "/workspace/") {
		return errEnvironmentFileCreatePath
	}
	return nil
}

// errUnknownEnvironmentFileField keeps the official code for an unknown field
// whose name is not echoed.
var errUnknownEnvironmentFileField = &fieldError{message: "Unknown parameter."}

// echoableField bounds the caller-supplied name that an error repeats in its
// message or param; see echotext.Allowed.
func echoableField(field string) bool { return echotext.Allowed(field) }

// unknownBodyField returns the first top-level member outside allowed, in
// document order. Malformed and non-object bodies are left to the caller's
// decoder, which reports them with the existing malformed-body error.
func unknownBodyField(raw []byte, allowed ...string) (string, bool) {
	if !json.Valid(raw) {
		return "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return "", false
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return "", false
		}
		if key, _ := token.(string); !slices.Contains(allowed, key) {
			return key, true
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return "", false
		}
	}
	return "", false
}
