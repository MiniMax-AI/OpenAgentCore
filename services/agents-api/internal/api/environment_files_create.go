package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

type EnvironmentFileWriter interface {
	WriteEnvironmentFile(context.Context, store.Environment, string, []byte) (int64, error)
}

func WithEnvironmentFileWriter(writer EnvironmentFileWriter) Option {
	return func(h *Handler) { h.fileWriter = writer }
}

// @Summary Create an Environment file from inline bytes or a source file
// @Description Uploads standard Base64 bytes to a file beneath /workspace in a qualified local Environment and returns 201. Accepts inline bytes or a project-owned source file_id through the same write path. Unknown body fields are rejected with their name as param. Basic public hosted creation requires explicit managed Runtime configuration; an openai_hosted Environment that has not connected yet returns 400. A private 50 MiB decoded-content limit applies. The parent directory must exist. Replacement installs a new mode-0600 inode; upstream overwrite metadata semantics remain unverified. Idle writes exclude execution. Missing receipts return unavailable and retain a durable mutation gate without automatic replay. Error/timing parity with upstream remains unverified.
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
	environment, err := h.store.GetEnvironment(r.Context(), tenantID(r), chi.URLParam(r, "environment_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	const maxJSON = int64(((proto.WorkspaceWriteMaxBytes+2)/3)*4 + (16 << 10))
	raw, ok := readJSONBodyLimit(w, r, maxJSON, "Inline upload exceeds this service's bounded file limit.")
	if !ok {
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
		if len(data) > proto.WorkspaceWriteMaxBytes {
			writeStoreError(w, r, store.ErrSourceFileTooLarge)
			return
		}
	}
	if !environmentFilesAccessible(w, environment) {
		return
	}
	if request.Type == "file_id" {
		if !h.sourceFilesAvailable(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		err = h.sourceFiles.ReadSourceFile(ctx, tenantID(r), *request.FileID, func(file store.SourceFile, body io.Reader) error {
			if file.SizeBytes > proto.WorkspaceWriteMaxBytes {
				return store.ErrSourceFileTooLarge
			}
			data, err = io.ReadAll(io.LimitReader(body, proto.WorkspaceWriteMaxBytes+1))
			if err == nil && int64(len(data)) != file.SizeBytes {
				return io.ErrUnexpectedEOF
			}
			return err
		})
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
	}
	if h.fileWriter == nil || !execution.LocalWorkspaceConfiguration(environment.Configuration) {
		writeStoreError(w, r, execution.ErrExecutionUnavailable)
		return
	}
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(215 * time.Second)); err != nil {
		writeStoreError(w, r, execution.ErrExecutionUnavailable)
		return
	}
	size, err := h.fileWriter.WriteEnvironmentFile(r.Context(), environment, strings.TrimPrefix(*request.Path, "/workspace/"), data)
	if err != nil {
		writeStoreError(w, r, err)
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

// echoableField bounds the caller-supplied name that an unknown-field error
// repeats in both message and param; JSON escaping can grow each byte sixfold.
// encoding/json has already replaced invalid bytes and lone surrogates with
// U+FFFD, so a name containing it is not repeated either.
func echoableField(field string) bool {
	if len(field) > 256 || !utf8.ValidString(field) || strings.ContainsRune(field, utf8.RuneError) {
		return false
	}
	for _, r := range field {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

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
