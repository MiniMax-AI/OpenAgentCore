package api

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

type pageOptions struct {
	after     string
	limit     int
	ascending bool
}

func readPage(w http.ResponseWriter, r *http.Request, extraKeys ...string) (pageOptions, bool) {
	return readPageSize(w, r, true, extraKeys...)
}

func readPageSize(w http.ResponseWriter, r *http.Request, rejectLarger bool, extraKeys ...string) (pageOptions, bool) {
	return readPageQuery(w, r, r.URL.Query(), rejectLarger, extraKeys...)
}

func readPageQuery(w http.ResponseWriter, r *http.Request, q url.Values, rejectLarger bool, extraKeys ...string) (pageOptions, bool) {
	return readPageQueryLimits(w, r, q, 20, 100, rejectLarger, extraKeys...)
}

func readPageQueryLimits(w http.ResponseWriter, r *http.Request, q url.Values, defaultLimit, maxLimit int, rejectLarger bool, extraKeys ...string) (pageOptions, bool) {
	keys := append([]string{"after", "limit", "order"}, extraKeys...)
	for key, values := range q {
		if !slices.Contains(keys, key) || len(values) != 1 {
			writeError(w, http.StatusBadRequest, "unsupported_parameter", "Supported list parameters are "+strings.Join(keys[:len(keys)-1], ", ")+" and "+keys[len(keys)-1]+", each supplied once.")
			return pageOptions{}, false
		}
	}
	limit, order := defaultLimit, q.Get("order")
	if raw, ok := q["limit"]; ok {
		var err error
		var requested int64
		requested, err = strconv.ParseInt(raw[0], 10, 64)
		if err != nil || requested < 1 || (rejectLarger && requested > int64(maxLimit)) {
			message := "limit must be a positive 64-bit integer."
			if rejectLarger {
				message = fmt.Sprintf("limit must be between 1 and %d.", maxLimit)
			}
			writeError(w, http.StatusBadRequest, "invalid_request", message)
			return pageOptions{}, false
		}
		limit = int(min(requested, int64(maxLimit)))
	}
	if _, supplied := q["order"]; supplied && order != "asc" && order != "desc" {
		writeListOrderError(w, r, order)
		return pageOptions{}, false
	}
	return pageOptions{after: strings.TrimSpace(q.Get("after")), limit: limit, ascending: order == "asc"}, true
}

func writeListOrderError(w http.ResponseWriter, r *http.Request, order string) {
	switch {
	case r.URL.Path == "/v1/files" || strings.HasPrefix(r.URL.Path, "/v1/files/"):
		writeError(w, http.StatusBadRequest, "", "order must be asc or desc.")
	case r.URL.Path == "/v1/skills" || strings.HasPrefix(r.URL.Path, "/v1/skills/"):
		writeError(w, http.StatusBadRequest, "invalid_value", fmt.Sprintf("Invalid value: '%s'. Supported values are: 'asc' and 'desc'.", order), "order")
	default:
		writeError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Failed to deserialize query string: order: unknown variant `%s`, expected `asc` or `desc`", order))
	}
}
