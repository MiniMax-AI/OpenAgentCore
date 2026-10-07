package api

import (
	"encoding/json"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

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
	var credential vaults.Credential
	switch credentialAuthType(request.Auth) {
	case "static_bearer":
		var auth v1.CredentialAuthInput
		if decodeInputObject(request.Auth, &auth, "type", "mcp_server_url", "token") != nil || auth.Token == nil || *auth.Token == "" || !credentialHTTPSURL(auth.MCPServerURL) {
			writeError(w, http.StatusBadRequest, "invalid_request", "static_bearer requires a nonempty string token and an absolute HTTPS mcp_server_url without userinfo or a fragment.")
			return
		}
		credential, err = h.Vaults.CreateStaticCredential(r.Context(), vaults.CreateStaticCredential{TenantID: tenantID(r), VaultID: vaultID, Name: name, MCPServerURL: *auth.MCPServerURL, Token: *auth.Token})
	case "mcp_oauth":
		command, parseErr := oauthCredentialCreate(request.Auth, name)
		if parseErr != nil {
			writeVaultsError(w, r, parseErr)
			return
		}
		command.TenantID, command.VaultID = tenantID(r), vaultID
		credential, err = h.Vaults.CreateOAuthCredential(r.Context(), command)
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "auth requires type static_bearer or mcp_oauth.")
		return
	}
	if err != nil {
		writeVaultsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, credentialResponse(credential))
}

func (h *Handler) getCredential(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := credentialResourceID(w, r, "vault_id")
	if !ok {
		return
	}
	id, ok := credentialResourceID(w, r, "credential_id")
	if !ok {
		return
	}
	credential, err := h.VaultsReader.GetCredential(r.Context(), tenantID(r), vaultID, id)
	if err != nil {
		writeVaultsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, credentialResponse(credential))
}

// credentialResourceID rejects a malformed Vault or Credential identifier with
// the not-found response. Use it only where the lookup is the next check.
func credentialResourceID(w http.ResponseWriter, r *http.Request, param string) (string, bool) {
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil || id == uuid.Nil {
		writeVaultsError(w, r, vaults.ErrNotFound)
		return "", false
	}
	return id.String(), true
}

func credentialResponse(c vaults.Credential) v1.Credential {
	auth := v1.CredentialAuth{Type: c.AuthType, MCPServerURL: c.MCPServerURL}
	if c.OAuth != nil {
		auth.ExpiresAt = c.OAuth.ExpiresAt
		if r := c.OAuth.Refresh; r != nil {
			auth.Refresh = &v1.OAuthCredentialRefresh{ClientID: r.ClientID, TokenEndpoint: r.TokenEndpoint, TokenEndpointAuth: v1.OAuthEndpointAuth{Type: r.TokenEndpointAuth}, Resource: r.Resource, Scope: r.Scope}
		}
	}
	return v1.Credential{ID: c.ID, VaultID: c.VaultID, Name: c.Name, Object: "vault.credential", Auth: auth, CreatedAt: c.CreatedAt.Unix(), UpdatedAt: c.UpdatedAt.Unix()}
}
