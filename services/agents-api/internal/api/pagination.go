package api

import (
	"net/http"
	"net/url"
	"strings"
)

type pageOptions struct {
	after     string
	limit     int
	ascending bool
}

// readPage rejects limits outside 1–100.
func readPage(w http.ResponseWriter, r *http.Request, extraKeys ...string) (pageOptions, bool) {
	return readPageQuery(w, r, r.URL.Query(), false, extraKeys...)
}

// readClampedPage clamps limit 0 to 1 and larger limits to 100.
func readClampedPage(w http.ResponseWriter, r *http.Request, extraKeys ...string) (pageOptions, bool) {
	return readPageQuery(w, r, r.URL.Query(), true, extraKeys...)
}

func readPageQuery(w http.ResponseWriter, r *http.Request, q url.Values, clamp bool, extraKeys ...string) (pageOptions, bool) {
	return readPageQueryLimits(w, r, q, 20, 100, clamp, extraKeys...)
}

// readPageQueryLimits ignores unknown keys, as the official lists do, but each
// supported key may appear only once.
func readPageQueryLimits(w http.ResponseWriter, r *http.Request, q url.Values, defaultLimit, maxLimit int, clamp bool, extraKeys ...string) (pageOptions, bool) {
	keys := append([]string{"after", "limit", "order"}, extraKeys...)
	for _, key := range keys {
		if len(q[key]) > 1 {
			writeListDuplicateError(w, r, key, keys)
			return pageOptions{}, false
		}
	}
	limit, order := defaultLimit, q.Get("order")
	if raw, supplied := q["limit"]; supplied {
		var valid bool
		if limit, valid = readListLimit(w, r, raw[0], maxLimit, clamp); !valid {
			return pageOptions{}, false
		}
	}
	if _, supplied := q["order"]; supplied && order != "asc" && order != "desc" {
		writeListOrderError(w, r, order)
		return pageOptions{}, false
	}
	return pageOptions{after: strings.TrimSpace(q.Get("after")), limit: limit, ascending: order == "asc"}, true
}
