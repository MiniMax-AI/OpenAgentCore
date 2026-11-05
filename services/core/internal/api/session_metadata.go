package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/metadata"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/go-chi/chi/v5"
)

func (h *Handler) updateSession(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONObject(w, r)
	if !ok || writeFieldError(w, validateBody(raw, updateAgentSessionParams)) {
		return
	}
	var request struct {
		Agent *struct {
			Model     *string `json:"model"`
			Reasoning *struct {
				Effort json.RawMessage `json:"effort"`
			} `json:"reasoning"`
			ServiceTier json.RawMessage `json:"service_tier"`
			Tools       json.RawMessage `json:"tools"`
		} `json:"agent"`
		Review   *string         `json:"review"`
		Metadata json.RawMessage `json:"metadata"`
	}
	_ = json.Unmarshal(raw, &request)
	if request.Agent != nil && request.Agent.Tools != nil {
		writeFieldError(w, &fieldError{param: "agent.tools", message: "Updating the Session tools is not supported."})
		return
	}
	if request.Agent == nil && request.Review == nil && request.Metadata == nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "At least one update field is required")
		return
	}
	command := sessions.UpdateSessionCommand{TenantID: tenantID(r), SessionID: chi.URLParam(r, "session_id")}
	if request.Metadata != nil {
		var values map[string]string
		_ = json.Unmarshal(request.Metadata, &values)
		command.Metadata = metadataOrEmpty(values)
		if writeFieldError(w, metadataFieldError(metadata.Validate(command.Metadata))) {
			return
		}
	}
	configure := func(engine string, stored json.RawMessage) (json.RawMessage, error) {
		var fields, agent, reasoning map[string]json.RawMessage
		_ = json.Unmarshal(stored, &fields) // The stored configuration was validated at creation.
		_ = json.Unmarshal(fields["agent"], &agent)
		_ = json.Unmarshal(agent["reasoning"], &reasoning)
		if reasoning == nil {
			reasoning = map[string]json.RawMessage{}
		}
		if a := request.Agent; a != nil {
			if a.Model != nil {
				if _, err := harnessconfig.ValidateModel(*a.Model); err != nil {
					return nil, &fieldError{param: "agent.model", message: err.Error()}
				}
				agent["model"], _ = json.Marshal(*a.Model)
			}
			if a.Reasoning != nil && a.Reasoning.Effort != nil {
				reasoning["effort"] = a.Reasoning.Effort
			}
			if a.ServiceTier != nil {
				agent["service_tier"] = a.ServiceTier
				if isNull(a.ServiceTier) {
					agent["service_tier"] = json.RawMessage(`"auto"`)
				}
			}
		}
		if request.Review != nil {
			delete(fields, "review")
			if *request.Review == "auto_review" {
				fields["review"], _ = json.Marshal(*request.Review)
			}
		}
		agent["reasoning"], _ = json.Marshal(reasoning)
		fields["agent"], _ = json.Marshal(agent)
		updated, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		if err := h.Policy.ValidateSessionUpdate(engine, stored, updated); err != nil {
			return nil, &fieldError{message: err.Error(), code: "unsupported_or_invalid_configuration"}
		}
		return updated, nil
	}
	if request.Agent != nil || request.Review != nil {
		command.Configure = configure
	}
	session, err := h.Sessions.UpdateSession(r.Context(), command)
	if err != nil {
		if !writeFieldError(w, err) {
			writeSessionsError(w, r, err)
		}
		return
	}
	h.respondSession(w, r, session)
}

func jsonValueKind(value json.RawMessage) string {
	value = bytes.TrimSpace(value)
	if len(value) == 0 {
		return "null"
	}
	switch value[0] {
	case '"':
		return "a string"
	case '{':
		return "an object"
	case '[':
		return "an array"
	case 't', 'f':
		return "a boolean"
	case 'n':
		return "null"
	}
	if bytes.ContainsAny(value, ".eE") {
		return "a number"
	}
	return "an integer"
}

func stringMetadata(values map[string]*string) (map[string]string, error) {
	metadata := make(map[string]string, len(values))
	for _, key := range slices.Sorted(maps.Keys(values)) {
		if values[key] == nil {
			return nil, &fieldError{param: "metadata." + key, message: fmt.Sprintf("Invalid type for 'metadata.%s': expected a string, but got null instead.", key)}
		}
		metadata[key] = *values[key]
	}
	return metadata, nil
}

// metadataOrEmpty keeps omitted and null metadata equal to an empty map.
func metadataOrEmpty(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

// metadataFieldError renders a metadata violation with the pinned messages
// and params. U+0000 is a documented local limit; the official service
// accepts it.
func metadataFieldError(err error) error {
	var violation *metadata.Violation
	if !errors.As(err, &violation) {
		return err
	}
	key := violation.Key
	switch violation.Kind {
	case metadata.TooManyPairs:
		return &fieldError{param: "metadata", message: fmt.Sprintf("Invalid 'metadata': too many properties. Expected an object with at most 16 properties, but got an object with %d properties instead.", violation.Length)}
	case metadata.KeyTooLong:
		return &fieldError{param: "metadata." + key, message: fmt.Sprintf("Invalid property name in 'metadata': '%s' is too long. Expected a string with maximum length 64, but got a string with length %d instead.", key, violation.Length)}
	case metadata.ValueTooLong:
		return &fieldError{param: "metadata." + key, message: fmt.Sprintf("Invalid 'metadata.%s': string too long. Expected a string with maximum length 512, but got a string with length %d instead.", key, violation.Length)}
	case metadata.KeyUnstorable:
		return &fieldError{param: "metadata." + key, message: fmt.Sprintf("Invalid property name in 'metadata': '%s' contains U+0000, which this service cannot store.", key)}
	case metadata.ValueUnstorable:
		return &fieldError{param: "metadata." + key, message: fmt.Sprintf("Invalid 'metadata.%s': string contains U+0000, which this service cannot store.", key)}
	}
	return err
}
