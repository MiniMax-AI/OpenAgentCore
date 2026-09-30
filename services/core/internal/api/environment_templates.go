package api

import (
	"context"
	"encoding/json"
	"net/http"
	"unicode/utf8"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/go-chi/chi/v5"
)

// EnvironmentTemplates manages Environment Templates. ResolveEnvironmentTemplate
// reads a Template with its initial files for Session creation.
type EnvironmentTemplates interface {
	ResolveEnvironmentTemplate(context.Context, string, string) (store.EnvironmentTemplate, []store.InitialFile, error)
	CreateEnvironmentTemplate(context.Context, string, store.EnvironmentTemplateInput) (store.EnvironmentTemplate, error)
	GetEnvironmentTemplate(context.Context, string, string) (store.EnvironmentTemplate, error)
	UpdateEnvironmentTemplate(context.Context, string, string, store.EnvironmentTemplateInput) (store.EnvironmentTemplate, error)
	DeleteEnvironmentTemplate(context.Context, string, string) (string, error)
	ListEnvironmentTemplates(context.Context, string, string, int, bool) (store.EnvironmentTemplatePage, error)
}

func decodeTemplateInput(raw []byte) (store.EnvironmentTemplateInput, error) {
	var fields map[string]json.RawMessage
	if decodeInputObject(raw, &fields, "name", "network", "capability_directories", "env", "files", "packages", "plugins", "skills", "setup_commands") != nil {
		return store.EnvironmentTemplateInput{}, store.ErrInvalidInput
	}
	in := store.EnvironmentTemplateInput{}
	if value, supplied := fields["name"]; supplied {
		in.SetName = true
		if json.Unmarshal(value, &in.Name) != nil || (in.Name != nil && (!utf8.ValidString(*in.Name) || utf8.RuneCountInString(*in.Name) < 1 || utf8.RuneCountInString(*in.Name) > 256)) {
			return in, store.ErrInvalidInput
		}
	}
	delete(fields, "name")
	_, in.SetNetwork = fields["network"]
	_, in.SetFiles = fields["files"]
	_, in.SetEnv = fields["env"]
	_, in.SetSetup = fields["setup_commands"]
	_, in.SetPackages = fields["packages"]
	_, in.SetSkills = fields["skills"]
	_, in.SetPlugins = fields["plugins"]
	_, in.SetDirectories = fields["capability_directories"]
	var setupErr error
	in.Initialization, setupErr = decodeEnvironmentSetup(fields)
	if setupErr != nil {
		return in, setupErr
	}
	var fileErr error
	in.Files, fileErr = decodeInitialFiles(fields["files"])
	if fileErr != nil {
		return in, fileErr
	}
	fields["type"] = json.RawMessage(`"openai_hosted"`)
	configuration, err := json.Marshal(fields)
	if err != nil {
		return in, err
	}
	environment, err := decodeHostedEnvironment(configuration)
	if err != nil {
		return in, err
	}
	in.NetworkAccess = environment.Network.Access
	in.AllowedDomains = append([]string{}, environment.Network.AllowedDomains...)
	return in, nil
}

func templateResponse(t store.EnvironmentTemplate) v1.EnvironmentTemplate {
	return v1.EnvironmentTemplate{ID: t.ID, Object: "agent.environment.template", Name: t.Name, CreatedAt: t.CreatedAt.Unix(), UpdatedAt: t.UpdatedAt.Unix(), CapabilityDirectories: append([]string{}, t.CapabilityDirectories...), Network: v1.EnvironmentNetwork{Access: t.NetworkAccess, AllowedDomains: append([]string{}, t.AllowedDomains...)}, Packages: packageMetadata(&t.Packages), Files: templateFileResponse(t.Files), Plugins: pluginResponse(t.Plugins), Skills: skillResponse(t.Skills)}
}

func readTemplateInput(w http.ResponseWriter, r *http.Request) (store.EnvironmentTemplateInput, bool) {
	raw, ok := readJSONObjectLimit(w, r, 16*1024*1024, "Request exceeds 16 MiB.")
	if !ok {
		return store.EnvironmentTemplateInput{}, false
	}
	in, err := decodeTemplateInput(raw)
	if writeFieldError(w, err) {
		return in, false
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsupported_or_invalid_configuration", "Template fields are invalid or require unsupported initialization. Name, enabled/disabled or exact-domain restricted network, initial files, env, npm/Python packages, setup commands inline/referenced Skill ZIPs, Plugin ZIPs and workspace capability directories are supported.")
		return in, false
	}
	return in, true
}

// @Summary Create an Environment Template
// @Description Saves tenant-owned hosted configuration. Supports nullable name, enabled/disabled or exact-domain restricted network, initial inline/file_id files, confidential env, ordered setup_commands, npm/Python packages inline/referenced Skill ZIPs, Plugin ZIPs and workspace-contained capability directories. Omitted/null network defaults to enabled. Restricted network requires 1–100 exact ASCII hostnames; other host forms and populated unsupported installations are rejected before persistence without echoing input. Network policy rejections return invalid_request_error with a null param. System dependencies must be preinstalled in the sandbox image or template, or on the host machine; packages.system is rejected. No compute is allocated. Exact hosted error/retry semantics remain unverified.
// @Tags Environment Templates
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param body body v1.EnvironmentTemplateRequest true "Reusable configuration"
// @Success 201 {object} v1.EnvironmentTemplate
// @Failure 400,401,413,500 {object} v1.ErrorResponse
// @Router /agents/environments/templates [post]
func (h *Handler) createEnvironmentTemplate(w http.ResponseWriter, r *http.Request) {
	in, ok := readTemplateInput(w, r)
	if !ok {
		return
	}
	value, err := h.EnvironmentTemplates.CreateEnvironmentTemplate(r.Context(), tenantID(r), in)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, templateResponse(value))
}

// @Summary Retrieve an Environment Template
// @Description Returns safe tenant-owned configuration metadata without allocating compute. Missing and foreign resources return the same not-found response.
// @Tags Environment Templates
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param environment_template_id path string true "Template ID"
// @Success 200 {object} v1.EnvironmentTemplate
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /agents/environments/templates/{environment_template_id} [get]
func (h *Handler) getEnvironmentTemplate(w http.ResponseWriter, r *http.Request) {
	value, err := h.EnvironmentTemplates.GetEnvironmentTemplate(r.Context(), tenantID(r), chi.URLParam(r, "environment_template_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, templateResponse(value))
}

// @Summary Update an Environment Template
// @Description Supplied fields replace atomically; omitted fields remain unchanged. Null name clears and null network resets to the pinned enabled default. Existing Session snapshots and creation retries remain unchanged. Initial files replace as a list; null/empty clears. File data is encrypted separately and excluded from response metadata. Skills replace as a list; null/empty clears. Skill archives are encrypted separately and omitted from responses. Plugins and capability directories replace as lists; null/empty clears. Plugin archives are encrypted and omitted from responses. Capability directories are snapshotted after setup. Environment MCP execution requires a qualified native transport and runtime network policy. Empty updates advance updated_at without changing saved fields or confidential contents. Network policy rejections return invalid_request_error with a null param.
// @Tags Environment Templates
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param environment_template_id path string true "Template ID"
// @Param body body v1.EnvironmentTemplateRequest true "Configuration replacements"
// @Success 200 {object} v1.EnvironmentTemplate
// @Failure 400,401,404,413,500 {object} v1.ErrorResponse
// @Router /agents/environments/templates/{environment_template_id} [post]
func (h *Handler) updateEnvironmentTemplate(w http.ResponseWriter, r *http.Request) {
	in, ok := readTemplateInput(w, r)
	if !ok {
		return
	}
	value, err := h.EnvironmentTemplates.UpdateEnvironmentTemplate(r.Context(), tenantID(r), chi.URLParam(r, "environment_template_id"), in)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, templateResponse(value))
}

// @Summary Delete an Environment Template
// @Description Deletes the tenant-owned reusable configuration without changing or deleting existing Sessions and their frozen configuration.
// @Tags Environment Templates
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param environment_template_id path string true "Template ID"
// @Success 200 {object} v1.EnvironmentTemplateDeleted
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /agents/environments/templates/{environment_template_id} [delete]
func (h *Handler) deleteEnvironmentTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := h.EnvironmentTemplates.DeleteEnvironmentTemplate(r.Context(), tenantID(r), chi.URLParam(r, "environment_template_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.EnvironmentTemplateDeleted{ID: id, Object: "agent.environment.template.deleted", Deleted: true})
}

// @Summary List Environment Templates
// @Description Lists tenant-owned safe template metadata in creation order with ID tie-breaking. Defaults to limit 20 and descending order; limit 0 is treated as 1 and larger limits as 100. Foreign, missing and malformed cursors return the same not found error. Concurrent-page and exact hosted error behavior remain unverified.
// @Tags Environment Templates
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param after query string false "Previous Template ID"
// @Param limit query integer false "Page size; 0 is treated as 1 and values above 100 as 100" default(20) minimum(0)
// @Param order query string false "Creation order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.EnvironmentTemplateList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /agents/environments/templates [get]
func (h *Handler) listEnvironmentTemplates(w http.ResponseWriter, r *http.Request) {
	options, ok := readClampedPage(w, r)
	if !ok {
		return
	}
	page, err := h.EnvironmentTemplates.ListEnvironmentTemplates(r.Context(), tenantID(r), options.after, options.limit, options.ascending)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response := v1.EnvironmentTemplateList{Object: "list", Data: make([]v1.EnvironmentTemplate, 0, len(page.Templates)), HasMore: page.HasMore}
	for _, value := range page.Templates {
		response.Data = append(response.Data, templateResponse(value))
	}
	if len(response.Data) > 0 {
		response.FirstID = &response.Data[0].ID
		response.LastID = &response.Data[len(response.Data)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}
