package api

import (
	"encoding/json"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// @Summary Create a Vault Credential
// @Description Stores static_bearer or mcp_oauth secrets as execution-owned authenticated ciphertext without contacting any endpoint. Static bearer and OAuth access tokens must be nonempty strings; their bytes are preserved. OAuth accepts a required access token, nullable RFC3339 expiry and optional refresh configuration with none, client_secret_basic or client_secret_post authentication. Required name is trimmed to 1–256 UTF-8 bytes. Credential and token endpoints require HTTPS without userinfo or fragments. Responses contain safe metadata only, including explicit nullable OAuth expiry, refresh, resource and scope. Missing encryption configuration returns local 503. External authorization and provider revocation remain caller responsibilities; exact hosted error/default semantics remain unverified.
// @Tags Credentials
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param vault_id path string true "Vault ID"
// @Param body body v1.CreateCredentialRequest true "Write-only credential authentication union"
// @Success 201 {object} v1.Credential
// @Failure 400,401,404,413,500,503 {object} v1.ErrorResponse
// @Router /vaults/{vault_id}/credentials [post]
func (h *Handler) createCredential(w http.ResponseWriter, r *http.Request) {
	vaultID := chi.URLParam(r, "vault_id")
	raw, ok := readJSONObject(w, r)
	if !ok {
		return
	}
	var request struct {
		Name *string         `json:"name"`
		Auth json.RawMessage `json:"auth"`
	}
	if decodeInputObject(raw, &request, "name", "auth") != nil || request.Name == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "name and supported auth are required.")
		return
	}
	name, err := normalizedVaultName(*request.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var credential store.Credential
	switch credentialAuthType(request.Auth) {
	case "static_bearer":
		var auth v1.CredentialAuthInput
		if decodeInputObject(request.Auth, &auth, "type", "mcp_server_url", "token") != nil || auth.Token == nil || *auth.Token == "" || !credentialHTTPSURL(auth.MCPServerURL) {
			writeError(w, http.StatusBadRequest, "invalid_request", "static_bearer requires a nonempty string token and an absolute HTTPS mcp_server_url without userinfo or a fragment.")
			return
		}
		credential, err = h.Vaults.CreateStaticCredential(r.Context(), tenantID(r), vaultID, store.CreateStaticCredentialInput{Name: name, MCPServerURL: *auth.MCPServerURL, Token: *auth.Token})
	case "mcp_oauth":
		input, parseErr := oauthCredentialCreate(request.Auth, name)
		if parseErr != nil {
			writeStoreError(w, r, parseErr)
			return
		}
		credential, err = h.Vaults.CreateOAuthCredential(r.Context(), tenantID(r), vaultID, input)
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "auth requires type static_bearer or mcp_oauth.")
		return
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, credentialResponse(credential))
}

// @Summary Retrieve safe Vault Credential metadata
// @Description Reads only non-secret metadata scoped to the authenticated project and owning Vault. No token decryption, network request or execution is performed. Unknown, foreign, wrong-Vault and malformed IDs use the same local not-found response; hosted error parity remains unverified.
// @Tags Credentials
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param vault_id path string true "Vault ID"
// @Param credential_id path string true "Credential ID"
// @Success 200 {object} v1.Credential
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /vaults/{vault_id}/credentials/{credential_id} [get]
func (h *Handler) getCredential(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := credentialResourceID(w, r, "vault_id")
	if !ok {
		return
	}
	id, ok := credentialResourceID(w, r, "credential_id")
	if !ok {
		return
	}
	credential, err := h.Vaults.GetCredential(r.Context(), tenantID(r), vaultID, id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, credentialResponse(credential))
}

// credentialResourceID rejects a malformed Vault or Credential identifier with
// the not-found response. Use it only where the lookup is the next check.
func credentialResourceID(w http.ResponseWriter, r *http.Request, param string) (string, bool) {
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil || id == uuid.Nil {
		writeStoreError(w, r, store.ErrNotFound)
		return "", false
	}
	return id.String(), true
}

func credentialResponse(c store.Credential) v1.Credential {
	auth := v1.CredentialAuth{Type: c.AuthType, MCPServerURL: c.MCPServerURL}
	if c.OAuth != nil {
		auth.ExpiresAt = c.OAuth.ExpiresAt
		if r := c.OAuth.Refresh; r != nil {
			auth.Refresh = &v1.OAuthCredentialRefresh{ClientID: r.ClientID, TokenEndpoint: r.TokenEndpoint, TokenEndpointAuth: v1.OAuthEndpointAuth{Type: r.TokenEndpointAuth}, Resource: r.Resource, Scope: r.Scope}
		}
	}
	return v1.Credential{ID: c.ID, VaultID: c.VaultID, Name: c.Name, Object: "vault.credential", Auth: auth, CreatedAt: c.CreatedAt.Unix(), UpdatedAt: c.UpdatedAt.Unix()}
}
