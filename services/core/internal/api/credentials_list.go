package api

import (
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/go-chi/chi/v5"
)

func (h *Handler) listCredentials(w http.ResponseWriter, r *http.Request) {
	vaultID := chi.URLParam(r, "vault_id")
	query, ok := readVaultPage(w, r)
	if !ok {
		return
	}
	page, err := h.VaultsReader.ListCredentials(r.Context(), tenantID(r), vaultID, query)
	if err != nil {
		writeVaultsError(w, r, err)
		return
	}
	response := v1.CredentialList{Object: "list", Data: make([]v1.Credential, 0, len(page.Credentials)), HasMore: page.NextCursor != ""}
	for _, credential := range page.Credentials {
		response.Data = append(response.Data, credentialResponse(credential))
	}
	if len(response.Data) > 0 {
		response.FirstID = &response.Data[0].ID
		response.LastID = &response.Data[len(response.Data)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}
