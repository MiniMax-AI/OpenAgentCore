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
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/go-chi/chi/v5"
)

// EnvironmentWorkspaces reads and writes a connected Environment's live
// workspace through its Runtime.
type EnvironmentWorkspaces interface {
	ReadEnvironmentDirectory(context.Context, sessions.Environment, string) (proto.WorkspaceDirectoryResult, error)
	WriteEnvironmentFile(context.Context, sessions.Environment, string, []byte) (int64, error)
}

func (h *Handler) listEnvironmentFiles(w http.ResponseWriter, r *http.Request) {
	environment, err := h.EnvironmentsReader.GetEnvironment(r.Context(), tenantID(r), chi.URLParam(r, "environment_id"))
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	options, ok := readEnvironmentFileQuery(w, r, environment)
	if !ok || !environmentFilesAccessible(w, environment) {
		return
	}
	if h.Execution == nil || !execution.LocalWorkspaceConfiguration(environment.Configuration) {
		writeSessionsError(w, r, execution.ErrExecutionUnavailable)
		return
	}
	// Allow the Worker's 45-second observation budget plus response delivery.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(50 * time.Second)); err != nil {
		writeSessionsError(w, r, execution.ErrExecutionUnavailable)
		return
	}
	result, err := h.Execution.Workspaces.ReadEnvironmentDirectory(r.Context(), environment, options.relativeDirectory)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	if result.Truncated || !proto.ValidWorkspaceDirectory(&result, proto.WorkspaceDirectoryMaxEntries) {
		writeSessionsError(w, r, execution.ErrExecutionUnavailable)
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
			writeSessionsError(w, r, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, response)
}

var errHostedEnvironmentProvisioning = &fieldError{message: "the hosted environment is still provisioning; wait until it is connected before accessing files"}

// environmentFilesAccessible rejects Files operations on an openai_hosted
// Environment whose first connection has not been observed (HE-18). Callers
// run it after the tenant-scoped lookup, so foreign Environments stay missing.
func environmentFilesAccessible(w http.ResponseWriter, environment sessions.Environment) bool {
	var configuration struct {
		Type string `json:"type"`
	}
	if environment.Status == "pending" && json.Unmarshal(environment.Configuration, &configuration) == nil && configuration.Type == "openai_hosted" {
		writeFieldError(w, errHostedEnvironmentProvisioning)
		return false
	}
	return true
}
