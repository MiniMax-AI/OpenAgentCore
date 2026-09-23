package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// listFamily selects the list-query bounds and error fields observed for a
// group of official collections. Agents API Beta lists are the default.
type listFamily int

const (
	betaList listFamily = iota
	filesList
	skillsList
)

func listFamilyOf(r *http.Request) listFamily {
	switch {
	case r.URL.Path == "/v1/files" || strings.HasPrefix(r.URL.Path, "/v1/files/"):
		return filesList
	case r.URL.Path == "/v1/skills" || strings.HasPrefix(r.URL.Path, "/v1/skills/"):
		return skillsList
	default:
		return betaList
	}
}

func writeListOrderError(w http.ResponseWriter, r *http.Request, order string) {
	switch listFamilyOf(r) {
	case filesList:
		writeError(w, http.StatusBadRequest, "", "order must be asc or desc.")
	case skillsList:
		// The observed message echoes the value; a long or unprintable one is left out.
		message := "Invalid value. Supported values are: 'asc' and 'desc'."
		if echoableField(order) {
			message = fmt.Sprintf("Invalid value: '%s'. Supported values are: 'asc' and 'desc'.", order)
		}
		writeError(w, http.StatusBadRequest, "invalid_value", message, "order")
	default:
		writeError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Failed to deserialize query string: order: unknown variant `%s`, expected `asc` or `desc`", order))
	}
}

func writeListDuplicateError(w http.ResponseWriter, r *http.Request, key string, keys []string) {
	switch listFamilyOf(r) {
	case filesList:
		// Only an identical repeated purpose was sampled; Files keep the local rejection.
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Supported list parameters are "+strings.Join(keys[:len(keys)-1], ", ")+" and "+keys[len(keys)-1]+", each supplied once.")
	case skillsList:
		writeError(w, http.StatusBadRequest, "duplicate_parameter", fmt.Sprintf("Duplicate parameter: '%[1]s'. You provided multiple values for this parameter, whereas only one is allowed. If you are trying to provide a list of values, use the array syntax instead e.g. '%[1]s[]=<value>'.", key), key)
	default:
		writeError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Failed to deserialize query string: duplicate field `%s`", key))
	}
}

// readListLimit applies a family's limit bounds. For Beta lists, clamp maps 0
// and values above maxLimit into range instead of rejecting them.
func readListLimit(w http.ResponseWriter, r *http.Request, raw string, maxLimit int, clamp bool) (int, bool) {
	requested, err := strconv.ParseInt(raw, 10, 64)
	switch listFamilyOf(r) {
	case filesList:
		message := fmt.Sprintf("limit must be between 1 and %d.", maxLimit)
		switch {
		case err != nil:
			writeError(w, http.StatusBadRequest, "invalid_request", message)
		case requested < 1 || requested > int64(maxLimit):
			// Hosted range errors have a null code; their schema text and detail member are not copied.
			writeError(w, http.StatusBadRequest, "", message)
		default:
			return int(requested), true
		}
	case skillsList:
		switch {
		case err != nil:
			writeError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("limit must be an integer between 0 and %d.", maxLimit))
		case requested < 0:
			writeError(w, http.StatusBadRequest, "integer_below_min_value", fmt.Sprintf("Invalid 'limit': integer below minimum value. Expected a value >= 0, but got %d instead.", requested), "limit")
		case requested > int64(maxLimit):
			writeError(w, http.StatusBadRequest, "integer_above_max_value", fmt.Sprintf("Invalid 'limit': integer above maximum value. Expected a value <= %d, but got %d instead.", maxLimit, requested), "limit")
		default:
			// Zero returns an empty page whose has_more reports remaining resources.
			return int(requested), true
		}
	default:
		// Beta lists deserialize an unsigned integer, so a sign is an invalid digit.
		switch {
		case strings.HasPrefix(raw, "-") || (err != nil && !errors.Is(err, strconv.ErrRange)):
			writeError(w, http.StatusBadRequest, "invalid_request_error", "Failed to deserialize query string: limit: invalid digit found in string")
		case err != nil:
			writeError(w, http.StatusBadRequest, "invalid_request_error", "Failed to deserialize query string: limit: number too large to fit in target type")
		case clamp:
			return int(max(1, min(requested, int64(maxLimit)))), true
		case requested < 1 || requested > int64(maxLimit):
			writeError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("limit must be between 1 and %d", maxLimit))
		default:
			return int(requested), true
		}
	}
	return 0, false
}
