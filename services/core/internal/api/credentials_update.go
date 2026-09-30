package api

import (
	"encoding/json"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
	"github.com/go-chi/chi/v5"
)

// @Summary Replace Vault Credential authentication secrets
// @Description Explicitly empty static bearer or OAuth access tokens and OAuth patches without a mutable field are rejected before storage. Omitted OAuth access tokens preserve the existing grant when expiry or refresh fields change. Updates the existing static_bearer or mcp_oauth authentication method without network requests. OAuth access_token omission/null retains the token; a new token clears omitted expiry, explicit null clears expiry, and other omitted fields remain unchanged. OAuth refresh patches cannot add configuration or change client, endpoint, resource or authentication method; nullable token/client-secret values retain stored secrets while explicit null scope clears scope. Whole-null refresh and token_endpoint_auth retain existing configuration under local policy. Identity, destination, creation time and Session bindings remain unchanged. Responses expose safe metadata only. Already-dispatched work is not revoked; provider revocation, storage-key rotation and exact hosted concurrent-update/error semantics remain separate.
// @Tags Credentials
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param vault_id path string true "Vault ID"
// @Param credential_id path string true "Credential ID"
// @Param body body v1.UpdateCredentialRequest true "Write-only credential authentication replacement union"
// @Success 200 {object} v1.Credential
// @Failure 400,401,404,413,500,503 {object} v1.ErrorResponse
// @Router /vaults/{vault_id}/credentials/{credential_id} [post]
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
