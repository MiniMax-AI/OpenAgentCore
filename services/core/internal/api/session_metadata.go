package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"

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
		Metadata json.RawMessage `json:"metadata"`
	}
	_ = json.Unmarshal(raw, &request)
	if len(request.Metadata) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "At least one update field is required")
		return
	}
	var values map[string]string
	_ = json.Unmarshal(request.Metadata, &values)
	values = metadataOrEmpty(values)
	if writeFieldError(w, metadataFieldError(metadata.Validate(values))) {
		return
	}
	session, err := h.Sessions.UpdateSessionMetadata(r.Context(), sessions.UpdateSessionMetadataCommand{TenantID: tenantID(r), SessionID: chi.URLParam(r, "session_id"), Metadata: values})
	if err != nil {
		writeSessionsError(w, r, err)
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
