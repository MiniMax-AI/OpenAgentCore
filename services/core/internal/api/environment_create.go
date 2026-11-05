package api

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// createEnvironment creates a standalone hosted Environment, prepared like a
// Session's and reusable by one Session at a time.
func (h *Handler) createEnvironment(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONObjectLimit(w, r, 16*1024*1024, "Request exceeds 16 MiB.")
	if !ok {
		return
	}
	if err := validateBody(raw, createAgentEnvironmentParams); err != nil {
		if !writeFieldError(w, err) {
			writeSessionsError(w, r, err)
		}
		return
	}
	var body struct {
		Environment json.RawMessage `json:"environment"`
		VaultIDs    []string        `json:"vault_ids"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil {
		writeFieldError(w, &fieldError{code: "invalid_request", message: "Request must be a JSON object containing supported fields."})
		return
	}
	if len(body.VaultIDs) > 0 {
		writeFieldError(w, &fieldError{param: "vault_ids", message: "Vault credentials attach to Sessions, not to standalone Environments."})
		return
	}
	var members map[string]json.RawMessage
	_ = json.Unmarshal(body.Environment, &members)
	// The create schema has neither member; they belong to the Environment resource.
	for _, name := range []string{"environment_id", "container_size"} {
		if members[name] != nil {
			writeFieldError(w, &fieldError{param: "environment." + name, message: "Unknown parameter: 'environment." + name + "'."})
			return
		}
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = uuid.NewString()
	} else if id, found, err := h.Execution.SessionAdmission.ReplayEnvironment(r.Context(), tenantID(r), sessionCreator(r), key, raw); err != nil {
		writeOperationError(w, r, err)
		return
	} else if found {
		h.writeEnvironmentCreated(w, r, id)
		return
	}
	var input sessionRequest
	err := input.decodeEnvironment(body.Environment, false)
	if err == nil && input.Environment.Type != "openai_hosted" {
		err = &fieldError{param: "environment.type", message: "A standalone Environment must be openai_hosted."}
	}
	if err != nil {
		if !writeFieldError(w, err) {
			writeSessionsError(w, r, err)
		}
		return
	}
	if input.templateID != "" {
		template, err := h.EnvironmentTemplatesReader.Resolve(r.Context(), tenantID(r), input.templateID)
		if err != nil {
			writeEnvironmentTemplatesError(w, r, err)
			return
		}
		if err := applyTemplateEnvironment(&input, template); err != nil {
			if !writeFieldError(w, err) {
				writeSessionsError(w, r, err)
			}
			return
		}
	}
	configuration, err := json.Marshal(input.Environment)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	id, err := h.Execution.SessionAdmission.CreateEnvironment(r.Context(), tenantID(r), sessions.CreateEnvironment{
		Creator: sessionCreator(r), IdempotencyKey: key, CreationRequest: raw,
		Configuration: configuration, Initialization: input.initialization, InitialFiles: input.initialFiles,
	})
	if err != nil {
		writeOperationError(w, r, err)
		return
	}
	h.writeEnvironmentCreated(w, r, id)
}

func (h *Handler) writeEnvironmentCreated(w http.ResponseWriter, r *http.Request, id string) {
	environment, err := h.EnvironmentsReader.GetEnvironment(r.Context(), tenantID(r), id)
	if err == nil {
		var response any
		if response, err = environmentResponse(environment); err == nil {
			writeJSON(w, http.StatusCreated, response)
			return
		}
	}
	writeSessionsError(w, r, err)
}
