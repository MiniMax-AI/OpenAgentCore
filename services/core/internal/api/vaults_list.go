package api

import (
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

func (h *Handler) listVaults(w http.ResponseWriter, r *http.Request) {
	query, ok := readVaultPage(w, r)
	if !ok {
		return
	}
	page, err := h.VaultsReader.ListVaults(r.Context(), tenantID(r), query)
	if err != nil {
		writeVaultsError(w, r, err)
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
