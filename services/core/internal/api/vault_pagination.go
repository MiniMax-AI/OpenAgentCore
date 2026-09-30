package api

import (
	"errors"
	"net/http"
	"slices"
	"strconv"

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
	// Vault and Credential limits also clamp negative and overflowing integers;
	// other values reach the shared Beta parser.
	if raw := q["limit"]; len(raw) == 1 {
		if requested, err := strconv.ParseInt(raw[0], 10, 64); err == nil || errors.Is(err, strconv.ErrRange) {
			q.Set("limit", strconv.FormatInt(max(1, min(requested, 100)), 10))
		}
	}
	options, ok := readPageQuery(w, r, q, true)
	return vaults.PageQuery{After: options.after, Limit: options.limit, Ascending: options.ascending, Statuses: statuses}, ok
}
