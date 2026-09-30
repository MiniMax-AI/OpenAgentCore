package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/go-chi/chi/v5"
)

// Environments reads Environments, grants and claims native installations, and
// manages the executor credentials of a Project's self_hosted Environments. The
// Project's principal is an executor credential's execution principal; the
// Core key that authorizes the request is not.
type Environments interface {
	GetEnvironment(context.Context, string, string) (sessions.Environment, error)
	AuthorizeEnvironmentInstallation(context.Context, identity.Principal, string, string) (string, int64, error)
	ValidateEnvironmentInstallation(context.Context, string, string) (sessions.InstallationAuthorization, error)
	ClaimEnvironmentInstallation(context.Context, string, string, string) error
	ProjectExecutorCredentialState(context.Context, identity.Principal, string) (sessions.ExecutorCredentialState, error)
	IssueProjectExecutorCredential(context.Context, identity.Principal, string, string, bool) (sessions.IssuedExecutorCredential, error)
	RevokeProjectExecutorCredential(context.Context, identity.Principal, string, string) error
}

// @Summary Retrieve an execution Environment
// @Description Returns durable connection status and safe installed metadata for supported self_hosted and basic openai_hosted profiles. Initial files expose frozen safe metadata without content; Plugin/Skill entries expose only safe configured installation metadata. Capability-directory discoveries are not added to those arrays. Unsupported installation configurations remain implementation gaps. This read does not prepare execution, start compute or require an enabled execution worker. Session deletion removes the associated Environment from public reads; project-shared read authorization is unchanged. Connection status does not prove native readiness or process quiescence.
// @Tags Environments
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param environment_id path string true "Environment ID"
// @Success 200 {object} v1.EnvironmentInfo
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /agents/environments/{environment_id} [get]
func (h *Handler) getEnvironment(w http.ResponseWriter, r *http.Request) {
	environment, err := h.Environments.GetEnvironment(r.Context(), tenantID(r), chi.URLParam(r, "environment_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response, err := environmentResponse(environment)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func environmentResponse(environment sessions.Environment) (v1.EnvironmentInfo, error) {
	configuration, err := storedEnvironment(environment.Configuration)
	if err != nil || (configuration.Type != "self_hosted" && configuration.Type != "openai_hosted") || environment.ID == "" {
		return v1.EnvironmentInfo{}, errors.New("unsupported stored environment metadata configuration")
	}
	switch environment.Status {
	case "pending", "connected", "disconnected", "expired", "failed":
	default:
		return v1.EnvironmentInfo{}, errors.New("unsupported stored environment resource status")
	}
	files := configuration.Files
	if files == nil {
		files = []json.RawMessage{}
	}
	if configuration.Skills == nil {
		configuration.Skills = []json.RawMessage{}
	}
	if configuration.Plugins == nil {
		configuration.Plugins = []json.RawMessage{}
	}
	return v1.EnvironmentInfo{
		ID: environment.ID, Object: "agent.environment", Type: configuration.Type, Status: environment.Status,
		Files: files, Plugins: configuration.Plugins, Skills: configuration.Skills,
	}, nil
}
