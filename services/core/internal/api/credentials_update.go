package api

import (
	"encoding/json"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
	"github.com/go-chi/chi/v5"
)

func (h *Handler) updateCredential(w http.ResponseWriter, r *http.Request) {
	vaultID, id := chi.URLParam(r, "vault_id"), chi.URLParam(r, "credential_id")
	raw, ok := readJSONObject(w, r)
	if !ok || writeFieldError(w, validateBody(raw, rotateVaultCredentialParams)) {
		return
	}
	var request struct {
		Auth v1.CredentialAuthReplacement `json:"auth"`
	}
	_ = json.Unmarshal(raw, &request)
	auth := request.Auth
	var credential vaults.Credential
	var err error
	if auth.Type == "static_bearer" {
		if writeFieldError(w, nonEmptyString("auth.token", *auth.Token)) {
			return
		}
		credential, err = h.Vaults.UpdateStaticCredential(r.Context(), vaults.UpdateStaticCredential{TenantID: tenantID(r), VaultID: vaultID, CredentialID: id, Token: *auth.Token})
	} else {
		command, fieldErr := oauthCredentialUpdate(auth)
		if writeFieldError(w, fieldErr) {
			return
		}
		command.TenantID, command.VaultID, command.CredentialID = tenantID(r), vaultID, id
		credential, err = h.Vaults.UpdateOAuthCredential(r.Context(), command)
	}
	if err != nil {
		writeVaultsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, credentialResponse(credential))
}
