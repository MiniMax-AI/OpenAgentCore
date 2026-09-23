package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

// @Summary Update execution Session metadata
// @Description The metadata field is required in an update body. Send null or {} to clear it, or supply an object to replace all pairs. Up to 16 string pairs, with keys at most 64 characters and values at most 512 characters; violations and non-string values return invalid_request_error with a metadata or metadata.<key> param. U+0000 is rejected as a local storage limit. Malformed, missing and foreign Session IDs share the not-found response. Execution configuration and activity are unchanged. Returns the same safe Environment and pending-input activity projection as Session retrieval.
// @Tags Sessions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param body body v1.UpdateSessionRequest true "Session metadata"
// @Success 200 {object} v1.Session
// @Failure 400,401,404,413,500 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id} [post]
func (h *Handler) updateSession(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONObject(w, r)
	if !ok {
		return
	}
	if writeFieldError(w, metadataTypeError(raw)) {
		return
	}
	var request struct {
		Metadata json.RawMessage `json:"metadata"`
	}
	if err := decodeInputObject(raw, &request, "metadata"); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request must be a JSON object containing supported fields.")
		return
	}
	if len(request.Metadata) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "At least one update field is required")
		return
	}
	var values map[string]*string
	if err := json.Unmarshal(request.Metadata, &values); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "metadata must be null or an object with string values.")
		return
	}
	metadata, err := stringMetadata(values)
	if err == nil {
		err = validateMetadata(metadata)
	}
	if err != nil {
		if !writeFieldError(w, err) {
			writeError(w, http.StatusBadRequest, "invalid_request", "metadata must be null or an object with string values.")
		}
		return
	}
	session, err := h.store.UpdateSessionMetadata(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), metadata)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.respondSession(w, r, session)
}

// metadataTypeError reports the first non-string value of a request body's
// top-level metadata object in document order, before generic body decoding
// can reject it. Other body and metadata shapes keep their existing errors.
// The shared body gate has already rejected repeated keys.
func metadataTypeError(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(objectMember(body, "metadata")))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil
	}
	for decoder.More() {
		token, err := decoder.Token()
		key, isKey := token.(string)
		var value json.RawMessage
		if err != nil || !isKey || decoder.Decode(&value) != nil {
			return nil
		}
		if kind := jsonValueKind(value); kind != "a string" {
			return &fieldError{param: "metadata." + key, message: fmt.Sprintf("Invalid type for 'metadata.%s': expected a string, but got %s instead.", key, kind)}
		}
	}
	return nil
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

// validateMetadata applies the pinned pair and character limits, then the
// local U+0000 storage limit. Sorted keys keep repeated errors stable.
func validateMetadata(metadata map[string]string) error {
	if len(metadata) > 16 {
		return &fieldError{param: "metadata", message: fmt.Sprintf("Invalid 'metadata': too many properties. Expected an object with at most 16 properties, but got an object with %d properties instead.", len(metadata))}
	}
	for _, key := range slices.Sorted(maps.Keys(metadata)) {
		if length := utf8.RuneCountInString(key); length > 64 {
			return &fieldError{param: "metadata." + key, message: fmt.Sprintf("Invalid property name in 'metadata': '%s' is too long. Expected a string with maximum length 64, but got a string with length %d instead.", key, length)}
		}
		if length := utf8.RuneCountInString(metadata[key]); length > 512 {
			return &fieldError{param: "metadata." + key, message: fmt.Sprintf("Invalid 'metadata.%s': string too long. Expected a string with maximum length 512, but got a string with length %d instead.", key, length)}
		}
	}
	return metadataCharacterError(metadata)
}

// metadataCharacterError rejects U+0000, which PostgreSQL text and jsonb cannot
// store. The official service accepts it; this is a documented local limit.
func metadataCharacterError(metadata map[string]string) error {
	for _, key := range slices.Sorted(maps.Keys(metadata)) {
		if strings.ContainsRune(key, 0) {
			return &fieldError{param: "metadata." + key, message: fmt.Sprintf("Invalid property name in 'metadata': '%s' contains U+0000, which this service cannot store.", key)}
		}
		if strings.ContainsRune(metadata[key], 0) {
			return &fieldError{param: "metadata." + key, message: fmt.Sprintf("Invalid 'metadata.%s': string contains U+0000, which this service cannot store.", key)}
		}
	}
	return nil
}
