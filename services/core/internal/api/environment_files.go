package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/go-chi/chi/v5"
)

type EnvironmentDirectoryReader interface {
	ReadEnvironmentDirectory(context.Context, store.Environment, string) (proto.WorkspaceDirectoryResult, error)
}

func WithEnvironmentDirectoryReader(reader EnvironmentDirectoryReader) Option {
	return func(h *Handler) { h.directoryReader = reader }
}

// @Summary List live Environment files
// @Description Lists direct regular files in one authorized self_hosted or qualified local workspace directory. Local paths use the public /workspace root and must be in cleaned form. This partial implementation defaults to the workspace root and limit 20; recursive scope and these defaults are not verified upstream semantics. A missing path, a regular file or a symbolic link returns an empty page; links are never followed. Daemons without a local workspace binding use the Claude SDK adapter reader, which keeps 404 for a missing path and 503 for a regular file or symbolic link. Well-formed unknown query keys are ignored; malformed query encoding and a repeated supported key are rejected. Sorts by case-sensitive path components, descending by default. Keep the same path, order and limit when using page. Each page rereads the complete bounded directory; changed file paths/sizes invalidate continuation locally with 400. There is no snapshot guarantee. An openai_hosted Environment that has not connected yet returns 400. Truncated or uncertain native results fail with 503 without returning a partial page. This read never starts a Turn or admits model input. Actual transport disconnect/reconnect events remain observable.
// @Tags Environments
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param environment_id path string true "Environment ID"
// @Param path query string false "Absolute directory in cleaned form inside /workspace"
// @Param limit query int false "Maximum file count; local default 20" minimum(1) maximum(100)
// @Param order query string false "Case-sensitive path-component order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Param page query string false "Opaque continuation token; keep path, order and limit unchanged"
// @Success 200 {object} v1.EnvironmentFileList
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/environments/{environment_id}/files [get]
func (h *Handler) listEnvironmentFiles(w http.ResponseWriter, r *http.Request) {
	environment, err := h.store.GetEnvironment(r.Context(), tenantID(r), chi.URLParam(r, "environment_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	options, ok := readEnvironmentFileQuery(w, r, environment)
	if !ok || !environmentFilesAccessible(w, environment) {
		return
	}
	if h.directoryReader == nil || !execution.LocalWorkspaceConfiguration(environment.Configuration) {
		writeStoreError(w, r, execution.ErrExecutionUnavailable)
		return
	}
	// Allow the Worker's 45-second observation budget plus response delivery.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(50 * time.Second)); err != nil {
		writeStoreError(w, r, execution.ErrExecutionUnavailable)
		return
	}
	result, err := h.directoryReader.ReadEnvironmentDirectory(r.Context(), environment, options.relativeDirectory)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if result.Truncated || !proto.ValidWorkspaceDirectory(&result, proto.WorkspaceDirectoryMaxEntries) {
		writeStoreError(w, r, execution.ErrExecutionUnavailable)
		return
	}
	files := make([]v1.EnvironmentFile, 0, len(result.Entries))
	for _, entry := range result.Entries {
		if entry.Kind == "file" {
			files = append(files, v1.EnvironmentFile{
				EnvironmentID: environment.ID, Object: "agent.environment.file",
				Path: path.Join(options.directory, entry.Name), SizeBytes: *entry.SizeBytes,
			})
		}
	}
	// All entries share one parent, so comparing their final components is sufficient.
	slices.SortFunc(files, func(a, b v1.EnvironmentFile) int {
		comparison := strings.Compare(a.Path, b.Path)
		if !options.ascending {
			return -comparison
		}
		return comparison
	})
	response, err := environmentFilePage(files, options)
	if err != nil {
		if !writeFieldError(w, err) {
			writeStoreError(w, r, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, response)
}

var errHostedEnvironmentProvisioning = &fieldError{message: "the hosted environment is still provisioning; wait until it is connected before accessing files"}

// environmentFilesAccessible rejects Files operations on an openai_hosted
// Environment whose first connection has not been observed (HE-18). Callers
// run it after the tenant-scoped lookup, so foreign Environments stay missing.
func environmentFilesAccessible(w http.ResponseWriter, environment store.Environment) bool {
	var configuration struct {
		Type string `json:"type"`
	}
	if environment.Status == "pending" && json.Unmarshal(environment.Configuration, &configuration) == nil && configuration.Type == "openai_hosted" {
		writeFieldError(w, errHostedEnvironmentProvisioning)
		return false
	}
	return true
}
