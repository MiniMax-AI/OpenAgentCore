package api

import (
	"bytes"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

func (h *Handler) deleteCredential(w http.ResponseWriter, r *http.Request) {
	body, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Credential deletion does not accept a request body.")
		return
	}
	vaultID, ok := credentialResourceID(w, r, "vault_id")
	if !ok {
		return
	}
	id, ok := credentialResourceID(w, r, "credential_id")
	if !ok {
		return
	}
	deleted, err := h.Vaults.DeleteCredential(r.Context(), vaults.DeleteCredential{TenantID: tenantID(r), VaultID: vaultID, CredentialID: id})
	if err != nil {
		writeVaultsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.CredentialDeleted{ID: deleted, Deleted: true, Object: "vault.credential.deleted"})
}
