package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

// SessionCreation creates Sessions without execution work and finds an
// earlier creation by its retry identity. Creation that admits work goes
// through Execution.SessionAdmission.
type SessionCreation interface {
	CreateSession(context.Context, string, sessions.CreateSession) (sessions.Creation, error)
	FindSessionCreation(context.Context, string, string, json.RawMessage, identity.Subject) (sessions.Creation, error)
}

// SessionAdmission creates Sessions that admit work through the execution
// Worker, which validates execution support first.
type SessionAdmission interface {
	CreateSession(context.Context, string, sessions.CreateSession) (sessions.Creation, error)
}

// Sessions updates and deletes Sessions, and records their public write audit.
type Sessions interface {
	UpdateSessionMetadata(context.Context, sessions.UpdateSessionMetadataCommand) (sessions.Session, error)
	DeleteSession(context.Context, sessions.DeleteSessionCommand) error
	AuditSessionOperation(context.Context, sessions.AuditSessionOperationCommand) error
}

// SessionsReader reads Sessions.
type SessionsReader interface {
	GetSession(context.Context, string, string) (sessions.Session, error)
	ListSessions(context.Context, string, string, int, bool, *string) (sessions.Page, error)
}

// routes builds the router. HEAD runs the GET route without a body after the
// same authentication and Beta checks (HP-19). Routes that stream events,
// download content, read a live workspace directory, sample Runtime
// observations or query Runtime history register an explicit HEAD 405 instead,
// so HEAD never holds a stream open, reads full content or does Runtime or
// telemetry work. Every 405, including unknown methods and routes outside the
// Beta group, has the JSON body and Allow header.
func (h *Handler) routes() *chi.Mux {
	router := chi.NewRouter()
	router.Use(h.responseHeaders, log.HTTPMiddleware, middleware.GetHead)
	router.MethodNotAllowed(methodNotAllowed)
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	registerOpenAPIDocsRoutes(router)
	router.Group(func(r chi.Router) {
		r.Use(h.authenticateProject)
		h.registerSkillRoutes(r)
		r.Post("/v1/files", h.createSourceFile)
		r.Get("/v1/files", h.listSourceFiles)
		r.Get("/v1/files/{file_id}", h.getSourceFile)
		r.Get("/v1/files/{file_id}/content", h.sourceFileContent)
		r.Head("/v1/files/{file_id}/content", methodNotAllowed)
		r.Delete("/v1/files/{file_id}", h.deleteSourceFile)
	})
	h.registerSandboxNodeRoutes(router)
	h.registerCoreRoutes(router)
	h.registerNativeInstallationRoutes(router)
	router.Route("/v1", func(r chi.Router) {
		r.Use(h.authenticate)
		r.Post("/vaults", h.createVault)
		r.Get("/vaults", h.listVaults)
		r.Get("/vaults/{vault_id}", h.getVault)
		r.Delete("/vaults/{vault_id}", h.deleteVault)
		r.Post("/vaults/{vault_id}/credentials", h.createCredential)
		r.Get("/vaults/{vault_id}/credentials", h.listCredentials)
		r.Get("/vaults/{vault_id}/credentials/{credential_id}", h.getCredential)
		r.Post("/vaults/{vault_id}/credentials/{credential_id}", h.updateCredential)
		r.Delete("/vaults/{vault_id}/credentials/{credential_id}", h.deleteCredential)
		r.Post("/agents", h.createAgent)
		r.Get("/agents", h.listAgents)
		r.Get("/agents/{agent_id}", h.getAgent)
		r.Post("/agents/{agent_id}", h.updateAgent)
		r.Delete("/agents/{agent_id}", h.deleteAgent)
		r.Post("/agents/environments/templates", h.createEnvironmentTemplate)
		r.Get("/agents/environments/templates", h.listEnvironmentTemplates)
		r.Get("/agents/environments/templates/{environment_template_id}", h.getEnvironmentTemplate)
		r.Post("/agents/environments/templates/{environment_template_id}", h.updateEnvironmentTemplate)
		r.Delete("/agents/environments/templates/{environment_template_id}", h.deleteEnvironmentTemplate)
		r.Get("/agents/environments/{environment_id}", h.getEnvironment)
		r.Get("/agents/environments/{environment_id}/files", h.listEnvironmentFiles)
		r.Head("/agents/environments/{environment_id}/files", methodNotAllowed)
		r.Post("/agents/environments/{environment_id}/files", h.createEnvironmentFile)
		r.Post("/agents/sessions", h.createSession)
		r.Get("/agents/sessions", h.listSessions)
		r.Get("/agents/sessions/{session_id}", h.getSession)
		r.Post("/agents/sessions/{session_id}", h.updateSession)
		r.Delete("/agents/sessions/{session_id}", h.deleteSession)
		r.Post("/agents/sessions/{session_id}/events", h.createEvents)
		r.Get("/agents/sessions/{session_id}/events", h.streamEvents)
		r.Head("/agents/sessions/{session_id}/events", methodNotAllowed)
		r.Get("/agents/sessions/{session_id}/items", h.listItems)
		r.Get("/agents/sessions/{session_id}/turns", h.listTurns)
		r.Get("/agents/sessions/{session_id}/turns/{turn_id}", h.getTurn)
		h.registerSubagentRoutes(r)
		r.Get("/agents/sessions/{session_id}/artifacts", h.listSessionArtifacts)
		r.Get("/agents/sessions/{session_id}/artifacts/{artifact_id}", h.getSessionArtifact)
		r.Get("/agents/sessions/{session_id}/artifacts/{artifact_id}/content", h.sessionArtifactContent)
		r.Head("/agents/sessions/{session_id}/artifacts/{artifact_id}/content", methodNotAllowed)
		r.Delete("/agents/sessions/{session_id}/artifacts/{artifact_id}", h.deleteSessionArtifact)
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "unsupported_operation", "This API operation is not supported.")
		})
		r.MethodNotAllowed(methodNotAllowed)
	})
	return router
}

// createSession atomically reserves or admits initial text with the Session.
func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONObjectLimit(w, r, 16*1024*1024, "Request exceeds 16 MiB.")
	if !ok {
		return
	}
	if writeFieldError(w, metadataTypeError(raw)) {
		return
	}
	var request decodedSessionRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	// An unknown member, including a case variant such as Metadata, is rejected
	// before decoding; see inexactMember.
	if inexactMember(raw, reflect.TypeOf(request)) || decoder.Decode(&request) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request must be a JSON object containing supported fields.")
		return
	}
	input, err := request.validated()
	if err != nil {
		if !writeFieldError(w, err) {
			writeError(w, http.StatusBadRequest, "invalid_request", "Request fields have invalid types or null values.")
		}
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = uuid.NewString()
	}
	initialInputs, err := initialSessionInputs(input.Input)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	if input.Environment.Type == "none" && len(initialInputs) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "conversation-only sessions currently require initial input")
		return
	}
	if input.Stream && input.Environment.Type != "self_hosted" && len(initialInputs) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "streaming session creation requires initial input")
		return
	}
	creationRequest, err := sessionCreationRequest(input, initialInputs)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	if h.recoverSessionCreation(w, r, key, creationRequest, input.Stream) {
		return
	}
	if input.templateID != "" {
		template, err := h.EnvironmentTemplatesReader.Resolve(r.Context(), tenantID(r), input.templateID)
		if err != nil {
			if !h.recoverSessionCreation(w, r, key, creationRequest, input.Stream) {
				writeEnvironmentTemplatesError(w, r, err)
			}
			return
		}
		if err := applyTemplateEnvironment(&input, template); err != nil {
			if !h.recoverSessionCreation(w, r, key, creationRequest, input.Stream) && !writeFieldError(w, err) {
				writeSessionsError(w, r, err)
			}
			return
		}
	}
	saved, inheritedProvider, err := h.sessionAgentDefaults(r.Context(), tenantID(r), input)
	if err != nil {
		if !h.recoverSessionCreation(w, r, key, creationRequest, input.Stream) {
			writeAgentsError(w, r, err)
		}
		return
	}
	err = h.prepareSessionModelConfiguration(r.Context(), &input, saved, inheritedProvider)
	var configuration json.RawMessage
	if err == nil {
		configuration, err = resolve(input, tenantID(r), key, saved)
	}
	if err == nil {
		configuration, err = freezeSessionHarnessConfig(configuration, input.resolvedHarnessConfig)
	}
	if err == nil {
		configuration, err = h.bindSessionCredentials(r.Context(), tenantID(r), configuration)
		if err != nil {
			if !h.recoverSessionCreation(w, r, key, creationRequest, input.Stream) {
				writeVaultsError(w, r, err)
			}
			return
		}
	}
	selectedEngine := h.Engine
	var provider *v1.ModelProviderInput
	var providerSource string
	var deploymentRevision uuid.UUID
	if err == nil {
		selectedEngine, provider, providerSource, deploymentRevision, err = h.resolveSessionExecution(r.Context(), input, inheritedProvider, configuration)
	}
	if err == nil {
		if invalid := h.Policy.ValidateSessionConfiguration(selectedEngine, configuration); invalid != nil {
			err = fmt.Errorf("Harness %s does not support the requested Agent/environment configuration: %w", selectedEngine, invalid)
		}
	}
	if err == nil {
		err = validateSessionModelConfiguration(selectedEngine, provider, configuration)
	}
	if err != nil {
		if h.recoverSessionCreation(w, r, key, creationRequest, input.Stream) {
			return
		}
		var required *modelProviderRequiredError
		switch {
		case errors.As(err, &required):
			writeError(w, http.StatusBadRequest, "model_provider_required", required.message, "x_agents_core.model_provider")
		case writeStoredDataError(w, r, err):
		case !writeFieldError(w, err):
			writeError(w, http.StatusBadRequest, "unsupported_or_invalid_configuration", err.Error())
		}
		return
	}
	if input.Environment.Type == "self_hosted" && h.Execution == nil {
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Self-hosted execution is not configured on this service.")
		return
	}
	if input.Environment.Type == "openai_hosted" && h.Sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Hosted execution is not configured on this service.")
		return
	}
	executionConfiguration := sessionExecutionProjection(input, saved, inheritedProvider, provider, selectedEngine, configuration)
	createInput := sessions.CreateSession{
		ExecutionConfiguration:     &executionConfiguration,
		ModelProvider:              provider,
		ModelProviderSource:        providerSource,
		DeploymentProviderRevision: deploymentRevision,
		Creator:                    sessionCreator(r), InitialFiles: input.initialFiles, Initialization: input.initialization,
		Engine: selectedEngine, IdempotencyKey: key, Metadata: input.Metadata, Configuration: configuration, InitialInputs: initialInputs, CreationRequest: creationRequest,
	}
	create := h.SessionCreation.CreateSession
	if len(initialInputs) > 0 || input.Environment.Type == "openai_hosted" {
		if h.Execution == nil {
			writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Execution input is not enabled on this service.")
			return
		}
		create = h.Execution.SessionAdmission.CreateSession
	}
	result, err := create(r.Context(), tenantID(r), createInput)
	if err != nil {
		writeOperationError(w, r, err)
		return
	}
	if input.Stream {
		h.respondSessionCreationStream(w, r, result)
		return
	}
	h.respondSessionStatus(w, r, result.Session, http.StatusCreated)
}

func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	session, err := h.SessionsReader.GetSession(r.Context(), tenantID(r), chi.URLParam(r, "session_id"))
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	h.respondSession(w, r, session)
}

func (h *Handler) respondSession(w http.ResponseWriter, r *http.Request, session sessions.Session) {
	h.respondSessionStatus(w, r, session, http.StatusOK)
}

func (h *Handler) respondSessionStatus(w http.ResponseWriter, r *http.Request, session sessions.Session, status int) {
	response, err := sessionResponse(session, h.executorURL())
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	if err := h.addSessionInstallation(w, r, &response); err != nil {
		writeSessionsError(w, r, err)
		return
	}
	writeJSON(w, status, response)
}

func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	options, ok := readClampedPage(w, r, "agent_id")
	if !ok {
		return
	}
	var agentID *string
	if values, present := r.URL.Query()["agent_id"]; present {
		agentID = &values[0]
	}
	page, err := h.SessionsReader.ListSessions(r.Context(), tenantID(r), options.after, options.limit, options.ascending, agentID)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	response := v1.SessionList{Data: make([]v1.Session, 0, len(page.Sessions)), HasMore: page.NextCursor != ""}
	for _, session := range page.Sessions {
		item, err := sessionResponse(session, h.executorURL())
		if err != nil {
			writeSessionsError(w, r, err)
			return
		}
		response.Data = append(response.Data, item)
	}
	writeJSON(w, http.StatusOK, sessionListResponse(response.Data, response.HasMore))
}
