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
	if !ok {
		return
	}
	var request struct {
		Auth json.RawMessage `json:"auth"`
	}
	if decodeInputObject(raw, &request, "auth") != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "auth with a supported type is required.")
		return
	}
	var credential vaults.Credential
	var err error
	switch credentialAuthType(request.Auth) {
	case "static_bearer":
		var auth v1.CredentialAuthReplacement
		if decodeInputObject(request.Auth, &auth, "type", "token") != nil || auth.Token == nil || *auth.Token == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "static_bearer auth requires a nonempty string token.")
			return
		}
		credential, err = h.Vaults.UpdateStaticCredential(r.Context(), vaults.UpdateStaticCredential{TenantID: tenantID(r), VaultID: vaultID, CredentialID: id, Token: *auth.Token})
	case "mcp_oauth":
		command, parseErr := oauthCredentialUpdate(request.Auth)
		if parseErr != nil {
			writeVaultsError(w, r, parseErr)
			return
		}
		command.TenantID, command.VaultID, command.CredentialID = tenantID(r), vaultID, id
		credential, err = h.Vaults.UpdateOAuthCredential(r.Context(), command)
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "auth requires type static_bearer or mcp_oauth.")
		return
	}
	if err != nil {
		writeVaultsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, credentialResponse(credential))
}
