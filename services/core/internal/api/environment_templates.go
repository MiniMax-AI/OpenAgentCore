package api

import (
	"context"
	"encoding/json"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmenttemplates"
	"github.com/go-chi/chi/v5"
)

// EnvironmentTemplates runs the Environment Template write use cases.
type EnvironmentTemplates interface {
	Create(context.Context, environmenttemplates.CreateCommand) (environmenttemplates.Template, error)
	Update(context.Context, environmenttemplates.UpdateCommand) (environmenttemplates.Template, error)
	Delete(context.Context, environmenttemplates.DeleteCommand) (string, error)
}

// EnvironmentTemplatesReader reads Environment Templates. Resolve also returns
// the decrypted configuration that Session creation composes.
type EnvironmentTemplatesReader interface {
	Get(ctx context.Context, tenantID, templateID string) (environmenttemplates.Template, error)
	List(ctx context.Context, tenantID string, query environmenttemplates.ListQuery) (environmenttemplates.Page, error)
	Resolve(ctx context.Context, tenantID, templateID string) (environmenttemplates.Resolved, error)
}

func decodeTemplateInput(raw []byte) (environmenttemplates.Input, error) {
	var fields map[string]json.RawMessage
	if decodeInputObject(raw, &fields, "name", "network", "capability_directories", "env", "files", "packages", "plugins", "skills", "setup_commands") != nil {
		return environmenttemplates.Input{}, environmenttemplates.ErrInvalidInput
	}
	in := environmenttemplates.Input{}
	if value, supplied := fields["name"]; supplied {
		in.SetName = true
		if json.Unmarshal(value, &in.Name) != nil || environmenttemplates.ValidateName(in.Name) != nil {
			return in, environmenttemplates.ErrInvalidInput
		}
	}
	delete(fields, "name")
	_, in.SetNetwork = fields["network"]
	_, in.SetFiles = fields["files"]
	_, in.SetEnv = fields["env"]
	_, in.SetCommands = fields["setup_commands"]
	_, in.SetPackages = fields["packages"]
	_, in.SetSkills = fields["skills"]
	_, in.SetPlugins = fields["plugins"]
	_, in.SetDirectories = fields["capability_directories"]
	var setupErr error
	in.Setup, setupErr = decodeEnvironmentSetup(fields)
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

func templateResponse(t environmenttemplates.Template) v1.EnvironmentTemplate {
	return v1.EnvironmentTemplate{ID: t.ID, Object: "agent.environment.template", Name: t.Name, CreatedAt: t.CreatedAt.Unix(), UpdatedAt: t.UpdatedAt.Unix(), CapabilityDirectories: append([]string{}, t.CapabilityDirectories...), Network: v1.EnvironmentNetwork{Access: t.NetworkAccess, AllowedDomains: append([]string{}, t.AllowedDomains...)}, Packages: packageMetadata(&t.Packages), Files: templateFileResponse(t.Files), Plugins: pluginResponse(t.Plugins), Skills: skillResponse(t.Skills)}
}

func readTemplateInput(w http.ResponseWriter, r *http.Request) (environmenttemplates.Input, bool) {
	raw, ok := readJSONObjectLimit(w, r, 16*1024*1024, "Request exceeds 16 MiB.")
	if !ok {
		return environmenttemplates.Input{}, false
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

func (h *Handler) createEnvironmentTemplate(w http.ResponseWriter, r *http.Request) {
	in, ok := readTemplateInput(w, r)
	if !ok {
		return
	}
	value, err := h.EnvironmentTemplates.Create(r.Context(), environmenttemplates.CreateCommand{TenantID: tenantID(r), Input: in})
	if err != nil {
		writeEnvironmentTemplatesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, templateResponse(value))
}

func (h *Handler) getEnvironmentTemplate(w http.ResponseWriter, r *http.Request) {
	value, err := h.EnvironmentTemplatesReader.Get(r.Context(), tenantID(r), chi.URLParam(r, "environment_template_id"))
	if err != nil {
		writeEnvironmentTemplatesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, templateResponse(value))
}

func (h *Handler) updateEnvironmentTemplate(w http.ResponseWriter, r *http.Request) {
	in, ok := readTemplateInput(w, r)
	if !ok {
		return
	}
	value, err := h.EnvironmentTemplates.Update(r.Context(), environmenttemplates.UpdateCommand{TenantID: tenantID(r), TemplateID: chi.URLParam(r, "environment_template_id"), Input: in})
	if err != nil {
		writeEnvironmentTemplatesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, templateResponse(value))
}

func (h *Handler) deleteEnvironmentTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := h.EnvironmentTemplates.Delete(r.Context(), environmenttemplates.DeleteCommand{TenantID: tenantID(r), TemplateID: chi.URLParam(r, "environment_template_id")})
	if err != nil {
		writeEnvironmentTemplatesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.EnvironmentTemplateDeleted{ID: id, Object: "agent.environment.template.deleted", Deleted: true})
}

func (h *Handler) listEnvironmentTemplates(w http.ResponseWriter, r *http.Request) {
	options, ok := readClampedPage(w, r)
	if !ok {
		return
	}
	page, err := h.EnvironmentTemplatesReader.List(r.Context(), tenantID(r), environmenttemplates.ListQuery{After: options.after, Limit: options.limit, Ascending: options.ascending})
	if err != nil {
		writeEnvironmentTemplatesError(w, r, err)
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
