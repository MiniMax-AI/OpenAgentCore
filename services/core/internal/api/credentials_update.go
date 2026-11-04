package api

import (
	"encoding/json"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/metadata"
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
		Auth     *v1.CredentialAuthReplacement `json:"auth"`
		Metadata map[string]string             `json:"metadata"`
	}
	_ = json.Unmarshal(raw, &request)
	if request.Auth == nil && request.Metadata == nil {
		writeFieldError(w, &fieldError{message: "At least one of auth or metadata is required."})
		return
	}
	if request.Auth != nil && writeFieldError(w, rejectEnvironmentVariable(request.Auth.Type)) || writeFieldError(w, metadataFieldError(metadata.Validate(request.Metadata))) {
		return
	}
	var credential vaults.Credential
	var err error
	if request.Auth == nil {
		credential, err = h.Vaults.UpdateCredentialMetadata(r.Context(), vaults.UpdateCredentialMetadata{TenantID: tenantID(r), VaultID: vaultID, CredentialID: id, Metadata: request.Metadata})
	} else if auth := *request.Auth; auth.Type == "static_bearer" {
		if writeFieldError(w, nonEmptyString("auth.token", *auth.Token)) {
			return
		}
		credential, err = h.Vaults.UpdateStaticCredential(r.Context(), vaults.UpdateStaticCredential{TenantID: tenantID(r), VaultID: vaultID, CredentialID: id, Token: *auth.Token, Metadata: request.Metadata})
	} else {
		command, fieldErr := oauthCredentialUpdate(auth)
		if writeFieldError(w, fieldErr) {
			return
		}
		command.TenantID, command.VaultID, command.CredentialID, command.Metadata = tenantID(r), vaultID, id, request.Metadata
		credential, err = h.Vaults.UpdateOAuthCredential(r.Context(), command)
	}
	if err != nil {
		writeVaultsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, credentialResponse(credential))
}
