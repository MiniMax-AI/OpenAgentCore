package api

import (
	"net/http"
	"slices"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

func readVaultPage(w http.ResponseWriter, r *http.Request) (vaults.PageQuery, bool) {
	q := r.URL.Query()
	if len(q["status"]) > 1 {
		writeListDuplicateError(w, r, "status", nil)
		return vaults.PageQuery{}, false
	}
	// A scalar status and status[] entries filter by their union.
	statuses := slices.Concat(q["status"], q["status[]"])
	for _, status := range statuses {
		if status != vaults.StatusActive && status != vaults.StatusArchived {
			writeError(w, http.StatusBadRequest, "invalid_request_error", "Failed to deserialize query string: status: data did not match any variant of untagged enum VaultStatusFilterParam")
			return vaults.PageQuery{}, false
		}
	}
	options, ok := readPageQuery(w, r, q, true)
	if !ok {
		return vaults.PageQuery{}, false
	}
	filter, ok := readMetadataFilter(w, r)
	return vaults.PageQuery{After: options.after, Limit: options.limit, Ascending: options.ascending, Statuses: statuses, Metadata: filter}, ok
}
