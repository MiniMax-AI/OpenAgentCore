package api

import (
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

// @Summary List Vaults
// @Description Lists project-owned Vaults independently of execution. An unknown, malformed or foreign after cursor returns not found. Includes active and archived records by default. Status accepts a scalar, the SDK's status[] array or both, filtering by their union; a repeated scalar is rejected. Limits default to 20 and clamp to 1–100. Equal creation times use ID ordering; exact hosted errors and concurrent-page behavior remain unverified. Archive/delete lifecycle is not implemented.
// @Tags Vaults
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param after query string false "Last Vault ID from the previous page"
// @Param limit query integer false "Requested page size, clamped to 1–100" default(20)
// @Param order query string false "Creation order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Param status query string false "Scalar status filter" Enums(active,archived)
// @Param status[] query []string false "Array status filter; combined with status as a union" collectionFormat(multi) Enums(active,archived)
// @Success 200 {object} v1.VaultList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /vaults [get]
func (h *Handler) listVaults(w http.ResponseWriter, r *http.Request) {
	options, statuses, ok := readVaultPage(w, r)
	if !ok {
		return
	}
	page, err := h.store.ListVaults(r.Context(), tenantID(r), options.after, options.limit, options.ascending, statuses)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response := v1.VaultList{Object: "list", Data: make([]v1.Vault, 0, len(page.Vaults)), HasMore: page.NextCursor != ""}
	for _, vault := range page.Vaults {
		response.Data = append(response.Data, vaultResponse(vault))
	}
	if len(response.Data) > 0 {
		response.FirstID = &response.Data[0].ID
		response.LastID = &response.Data[len(response.Data)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}
