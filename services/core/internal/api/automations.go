package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/automations"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/metadata"
	"github.com/go-chi/chi/v5"
)

// Automations runs the Automation use cases and the delivery pass.
type Automations interface {
	Create(context.Context, automations.Automation) (automations.Automation, error)
	Update(ctx context.Context, tenantID, id string, patch automations.Patch) (automations.Automation, error)
	Pause(ctx context.Context, tenantID, id string) (automations.Automation, error)
	Resume(ctx context.Context, tenantID, id string) (automations.Automation, error)
	Delete(ctx context.Context, tenantID, id string) (string, error)
	Invoke(ctx context.Context, tenantID, id string) (automations.Invocation, error)
	Record(context.Context, time.Time) (int, error)
	Deliverable(ctx context.Context, limit int) ([]automations.Delivery, error)
	Settle(context.Context, automations.Outcome) error
}

const maxInstructions = 1 << 20

func (h *Handler) registerAutomationRoutes(r chi.Router) {
	const base = "/agents/automations"
	r.Post(base, h.createAutomation)
	r.Get(base, h.listAutomations)
	r.Get(base+"/{automation_id}", h.getAutomation)
	r.Post(base+"/{automation_id}", h.updateAutomation)
	r.Delete(base+"/{automation_id}", h.deleteAutomation)
	r.Post(base+"/{automation_id}/pause", h.pauseAutomation)
	r.Post(base+"/{automation_id}/resume", h.resumeAutomation)
	r.Post(base+"/{automation_id}/invocations", h.invokeAutomation)
	r.Get(base+"/{automation_id}/invocations", h.listAutomationInvocations)
	r.Get(base+"/{automation_id}/invocations/{invocation_id}", h.getAutomationInvocation)
}

// automationTarget is a validated target: its type, the safe projection the
// resource shows and the request the delivery replays.
type automationTarget struct {
	kind                 string
	projection, contents json.RawMessage
	// report writes the response for the error that came with the target.
	report func(http.ResponseWriter, *http.Request, error)
}

func (h *Handler) createAutomation(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONObjectLimit(w, r, 16*1024*1024, "Request exceeds 16 MiB.")
	if !ok {
		return
	}
	var kind struct {
		Type json.RawMessage `json:"type"`
	}
	_ = json.Unmarshal(raw, &kind)
	pinned := createAutomationParamsPeriodic
	switch string(kind.Type) {
	case `"once"`:
		pinned = createAutomationParamsOnce
	case `"periodic"`, "":
	default:
		if writeFieldError(w, checkValue("type", kind.Type, shape{kind: enumValue, values: []string{automations.Periodic, automations.Once}})) {
			return
		}
	}
	if writeFieldError(w, validateBody(raw, pinned)) {
		return
	}
	var body struct {
		Type       string            `json:"type"`
		Name       string            `json:"name"`
		Metadata   map[string]string `json:"metadata"`
		Expression string            `json:"expression"`
		Timezone   string            `json:"timezone"`
		RunAt      int64             `json:"run_at"`
		Target     json.RawMessage   `json:"target"`
	}
	_ = json.Unmarshal(raw, &body)
	if err := automationFields(&body.Name, body.Metadata); err != nil {
		writeFieldError(w, err)
		return
	}
	target, err := h.automationTarget(r, body.Target)
	if err != nil {
		target.report(w, r, err)
		return
	}
	a := automations.Automation{TenantID: tenantID(r), Type: body.Type, Name: body.Name, Metadata: metadataOrEmpty(body.Metadata), Expression: body.Expression, Timezone: body.Timezone, TargetType: target.kind, Target: target.projection, Contents: target.contents}
	if body.Type == automations.Once {
		runAt, err := automations.RunAt(body.RunAt)
		if err != nil {
			writeAutomationsError(w, r, err)
			return
		}
		a.RunAt = &runAt
	}
	created, err := h.Automations.Create(r.Context(), a)
	if err != nil {
		writeAutomationsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created.Resource())
}

func (h *Handler) updateAutomation(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONObjectLimit(w, r, 16*1024*1024, "Request exceeds 16 MiB.")
	if !ok || writeFieldError(w, validateBody(raw, updateAutomationParams)) {
		return
	}
	var body struct {
		Name       *string           `json:"name"`
		Metadata   map[string]string `json:"metadata"`
		Expression *string           `json:"expression"`
		Timezone   *string           `json:"timezone"`
		RunAt      *int64            `json:"run_at"`
		Target     json.RawMessage   `json:"target"`
	}
	_ = json.Unmarshal(raw, &body)
	patch := automations.Patch{Name: body.Name, Metadata: body.Metadata, Expression: body.Expression, Timezone: body.Timezone}
	if err := automationFields(body.Name, body.Metadata); err != nil {
		writeFieldError(w, err)
		return
	}
	if body.RunAt != nil {
		runAt, err := automations.RunAt(*body.RunAt)
		if err != nil {
			writeAutomationsError(w, r, err)
			return
		}
		patch.RunAt = &runAt
	}
	if body.Target != nil {
		target, err := h.automationTarget(r, body.Target)
		if err != nil {
			target.report(w, r, err)
			return
		}
		patch.TargetType, patch.Target, patch.Contents = target.kind, target.projection, target.contents
	}
	updated, err := h.Automations.Update(r.Context(), tenantID(r), chi.URLParam(r, "automation_id"), patch)
	if err != nil {
		writeAutomationsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, updated.Resource())
}

func (h *Handler) getAutomation(w http.ResponseWriter, r *http.Request) {
	a, err := h.AutomationsReader.Get(r.Context(), tenantID(r), chi.URLParam(r, "automation_id"))
	if err != nil {
		writeAutomationsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.Resource())
}

func (h *Handler) listAutomations(w http.ResponseWriter, r *http.Request) {
	options, ok := readClampedPage(w, r, "session_id")
	if !ok {
		return
	}
	// A supplied empty session_id matches no Automation, as an empty agent_id matches no Session.
	var sessionID *string
	if values, present := r.URL.Query()["session_id"]; present {
		sessionID = &values[0]
	}
	page, err := h.AutomationsReader.List(r.Context(), tenantID(r), automations.ListQuery{After: options.after, Limit: options.limit, Ascending: options.ascending, SessionID: sessionID})
	if err != nil {
		writeAutomationsError(w, r, err)
		return
	}
	response := v1.AutomationList{Object: "list", Data: make([]v1.Automation, 0, len(page.Automations)), HasMore: page.HasMore}
	for _, a := range page.Automations {
		response.Data = append(response.Data, a.Resource())
	}
	if len(response.Data) > 0 {
		response.FirstID, response.LastID = &response.Data[0].ID, &response.Data[len(response.Data)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	id, err := h.Automations.Delete(r.Context(), tenantID(r), chi.URLParam(r, "automation_id"))
	if err != nil {
		writeAutomationsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.AutomationDeleted{ID: id, Object: "agent.automation.deleted", Deleted: true})
}

func (h *Handler) pauseAutomation(w http.ResponseWriter, r *http.Request) {
	h.changeAutomation(w, r, h.Automations.Pause)
}

func (h *Handler) resumeAutomation(w http.ResponseWriter, r *http.Request) {
	h.changeAutomation(w, r, h.Automations.Resume)
}

func (h *Handler) changeAutomation(w http.ResponseWriter, r *http.Request, change func(context.Context, string, string) (automations.Automation, error)) {
	a, err := change(r.Context(), tenantID(r), chi.URLParam(r, "automation_id"))
	if err != nil {
		writeAutomationsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.Resource())
}

func (h *Handler) invokeAutomation(w http.ResponseWriter, r *http.Request) {
	invocation, err := h.Automations.Invoke(r.Context(), tenantID(r), chi.URLParam(r, "automation_id"))
	if err != nil {
		writeAutomationsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, invocation.Resource())
}

func (h *Handler) getAutomationInvocation(w http.ResponseWriter, r *http.Request) {
	invocation, err := h.AutomationsReader.GetInvocation(r.Context(), tenantID(r), chi.URLParam(r, "automation_id"), chi.URLParam(r, "invocation_id"))
	if err != nil {
		writeAutomationsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, invocation.Resource())
}

func (h *Handler) listAutomationInvocations(w http.ResponseWriter, r *http.Request) {
	options, ok := readClampedPage(w, r)
	if !ok {
		return
	}
	page, err := h.AutomationsReader.ListInvocations(r.Context(), tenantID(r), chi.URLParam(r, "automation_id"), automations.ListQuery{After: options.after, Limit: options.limit, Ascending: options.ascending})
	if err != nil {
		writeAutomationsError(w, r, err)
		return
	}
	response := v1.AutomationInvocationList{Object: "list", Data: make([]v1.AutomationInvocation, 0, len(page.Invocations)), HasMore: page.HasMore}
	for _, invocation := range page.Invocations {
		response.Data = append(response.Data, invocation.Resource())
	}
	if len(response.Data) > 0 {
		response.FirstID, response.LastID = &response.Data[0].ID, &response.Data[len(response.Data)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}

// automationFields checks the name and metadata of a request. A nil name is
// not checked.
func automationFields(name *string, values map[string]string) error {
	if name != nil {
		switch n := utf8.RuneCountInString(*name); {
		case n < 1:
			return &fieldError{param: "name", message: "Invalid 'name': empty string. Expected a string with minimum length 1, but got an empty string instead."}
		case n > 256:
			return &fieldError{param: "name", message: fmt.Sprintf("Invalid 'name': string too long. Expected a string with maximum length 256, but got a string with length %d instead.", n)}
		}
	}
	return metadataFieldError(metadata.Validate(values))
}

// prefixTarget names a Session create field error under the target.
func prefixTarget(err error) error {
	var field *fieldError
	if !errors.As(err, &field) {
		return err
	}
	param := "target"
	if field.param != "" {
		param += "." + field.param
	}
	return &fieldError{param: param, message: field.message, code: field.code}
}

// writeTargetError reports a target that failed validation: a field error, or
// an error of the Session lookup rules the target reuses.
func writeTargetError(w http.ResponseWriter, r *http.Request, err error) {
	if !writeFieldError(w, err) {
		writeSessionsError(w, r, err)
	}
}

// automationTarget validates a target and builds what is saved. A new_session
// target is validated as the Session create body it replays; an
// existing_session target must name a Session and hold deliverable text.
func (h *Handler) automationTarget(r *http.Request, raw json.RawMessage) (automationTarget, error) {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	var kind string
	_ = json.Unmarshal(fields["type"], &kind)
	if kind == "existing_session" {
		return h.existingSessionTarget(r, raw)
	}
	if kind != "new_session" && fields["type"] != nil {
		if err := checkValue("target.type", fields["type"], shape{kind: enumValue, values: []string{"new_session", "existing_session"}}); err != nil {
			return automationTarget{report: writeTargetError}, err
		}
	}
	return h.newSessionTarget(r, raw, fields)
}

func (h *Handler) existingSessionTarget(r *http.Request, raw json.RawMessage) (automationTarget, error) {
	fail := automationTarget{report: writeTargetError}
	if err := checkValue("target", raw, automationTargetParamExistingSession); err != nil {
		return fail, err
	}
	var target struct {
		SessionID    string `json:"session_id"`
		Instructions string `json:"instructions"`
	}
	_ = json.Unmarshal(raw, &target)
	if utf8.RuneCountInString(target.Instructions) > maxInstructions {
		return fail, &fieldError{param: "target.instructions", message: "instructions must not exceed 1048576 characters."}
	}
	inputs, err := executionInputs([]json.RawMessage{instructionsEvent(target.Instructions)})
	if err != nil {
		return fail, &fieldError{param: "target.instructions", message: "instructions must be non-empty text this service can store."}
	}
	session, err := h.SessionsReader.GetSession(r.Context(), tenantID(r), target.SessionID)
	if err != nil {
		return fail, err
	}
	if h.Policy.ValidateEngineInputs(session.Engine, session.Configuration, inputs) != nil {
		return fail, &fieldError{param: "target.instructions", message: "The Session's Harness does not accept these instructions."}
	}
	// The saved target names the Session by its canonical id, whatever form the request used.
	contents, _ := json.Marshal(map[string]string{"session_id": session.ID, "instructions": target.Instructions})
	projection, _ := json.Marshal(v1.AutomationTarget{Type: "existing_session", SessionID: &session.ID, Instructions: &target.Instructions})
	return automationTarget{kind: "existing_session", projection: projection, contents: contents}, nil
}

// instructionsEvent is the input an existing_session delivery sends: instructions as
// one user message.
func instructionsEvent(instructions string) json.RawMessage {
	event, _ := json.Marshal(map[string]any{"type": "agent.session.input.message", "input": []v1.InputMessage{{Type: "message", Role: "user", Content: []v1.InputContent{{Type: "input_text", Text: &instructions}}}}})
	return event
}

func (h *Handler) newSessionTarget(r *http.Request, raw json.RawMessage, fields map[string]json.RawMessage) (automationTarget, error) {
	fail := automationTarget{report: writeTargetError}
	if err := checkValue("target", raw, automationTargetParamNewSession); err != nil {
		return fail, err
	}
	_, inline := fields["agent"]
	_, saved := fields["agent_id"]
	if inline == saved {
		return fail, &fieldError{param: "target", message: "Provide exactly one of agent and agent_id."}
	}
	if inline {
		var agent map[string]json.RawMessage
		_ = json.Unmarshal(fields["agent"], &agent)
		if agent["x_agents_core"] != nil {
			return fail, &fieldError{param: "target.agent.x_agents_core", message: "x_agents_core is not supported in an automation target."}
		}
	}
	delete(fields, "type")
	contents, _ := json.Marshal(fields)
	input, _, err := parseSessionRequest(contents)
	if err != nil {
		return fail, prefixTarget(err)
	}
	target := v1.AutomationTarget{Type: "new_session", AgentID: input.AgentID, Environment: automationEnvironment(input), VaultIDs: input.VaultIDs, Metadata: input.Metadata}
	_ = json.Unmarshal(fields["input"], &target.Input)
	for i := range target.Input {
		target.Input[i].Type = "message"
	}
	plan, report, err := h.planSession(r.Context(), tenantID(r), "", &input)
	if err != nil {
		var required *modelProviderRequiredError
		if errors.As(err, &required) {
			report = func(w http.ResponseWriter, _ *http.Request, _ error) {
				writeError(w, http.StatusBadRequest, "model_provider_required", required.message, "target")
			}
		}
		return automationTarget{report: report}, prefixTarget(err)
	}
	if inline {
		var cfg configuration
		_ = json.Unmarshal(plan.configuration, &cfg)
		agent := cfg.Agent
		tools, err := resourceTools(cfg)
		if err != nil {
			return fail, err
		}
		target.Agent = &v1.AutomationAgentConfig{Model: agent.Model, Reasoning: v1.ReasoningResource{Effort: agent.Reasoning.Effort, Summary: agent.Reasoning.Summary}, Text: agent.Text, ServiceTier: agent.ServiceTier, Instructions: agent.Instructions, Tools: tools, MultiAgent: agent.MultiAgent, Compaction: agent.Compaction, Experimental: agent.Experimental}
	}
	projection, err := json.Marshal(target)
	return automationTarget{kind: "new_session", projection: projection, contents: contents}, err
}

// automationEnvironment projects the environment the request supplied. A member
// the request left out stays nil, which the resource shows as null: inherit. A
// self_hosted environment always lists its capability directories.
func automationEnvironment(input sessionRequest) *v1.AutomationEnvironment {
	env := input.Environment
	result := &v1.AutomationEnvironment{Type: env.Type}
	var supplied map[string]json.RawMessage
	_ = json.Unmarshal(input.originalEnvironment, &supplied)
	has := func(name string) bool { value, ok := supplied[name]; return ok && string(value) != "null" }
	if has("environment_template_id") {
		result.EnvironmentTemplateID = &input.templateID
	}
	if has("packages") && env.Packages != nil {
		packages := packageMetadata(env.Packages)
		result.Packages = &packages
	}
	if has("network") && env.Network != nil {
		result.Network = &v1.EnvironmentNetwork{Access: env.Network.Access, AllowedDomains: append([]string{}, env.Network.AllowedDomains...)}
	}
	if has("desktop") {
		result.Desktop = json.RawMessage(`{"enabled":false}`)
	}
	if has("capability_directories") {
		result.CapabilityDirectories = env.CapabilityDirectories
	}
	if result.CapabilityDirectories == nil && env.Type == "self_hosted" {
		result.CapabilityDirectories = []string{}
	}
	if has("skills") {
		result.Skills = env.Skills
	}
	if has("plugins") {
		result.Plugins = env.Plugins
	}
	if has("files") {
		result.Files = env.Files
	}
	if env.WorkspaceDirectory != "" {
		result.WorkspaceDirectory = &env.WorkspaceDirectory
	}
	return result
}
