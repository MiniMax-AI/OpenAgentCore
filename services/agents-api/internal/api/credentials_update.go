package api

import (
	"encoding/json"
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
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
	vaultID, ok := credentialResourceID(w, r, "vault_id")
	if !ok {
		return
	}
	id, ok := credentialResourceID(w, r, "credential_id")
	if !ok {
		return
	}
	raw, ok := readJSONBody(w, r)
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
	var credential store.Credential
	var err error
	switch credentialAuthType(request.Auth) {
	case "static_bearer":
		var auth v1.CredentialAuthReplacement
		if decodeInputObject(request.Auth, &auth, "type", "token") != nil || auth.Token == nil || *auth.Token == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "static_bearer auth requires a nonempty string token.")
			return
		}
		credential, err = h.store.UpdateStaticCredential(r.Context(), tenantID(r), vaultID, id, store.UpdateStaticCredentialInput{Token: *auth.Token})
	case "mcp_oauth":
		input, parseErr := oauthCredentialUpdate(request.Auth)
		if parseErr != nil {
			writeStoreError(w, r, parseErr)
			return
		}
		credential, err = h.store.UpdateOAuthCredential(r.Context(), tenantID(r), vaultID, id, input)
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "auth requires type static_bearer or mcp_oauth.")
		return
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, credentialResponse(credential))
}
