package api

import (
	"cmp"
	"encoding/json"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/metadata"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (h *Handler) createCredential(w http.ResponseWriter, r *http.Request) {
	vaultID := chi.URLParam(r, "vault_id")
	raw, ok := readJSONObject(w, r)
	if !ok || writeFieldError(w, validateBody(raw, createVaultCredentialParams)) {
		return
	}
	var request struct {
		Name     string            `json:"name"`
		Auth     json.RawMessage   `json:"auth"`
		Metadata map[string]string `json:"metadata"`
	}
	_ = json.Unmarshal(raw, &request)
	name, err := normalizedVaultName(request.Name)
	if writeFieldError(w, err) {
		return
	}
	var auth v1.CredentialAuthInput
	_ = json.Unmarshal(request.Auth, &auth)
	if writeFieldError(w, cmp.Or(rejectEnvironmentVariable(auth.Type), metadataFieldError(metadata.Validate(request.Metadata)))) {
		return
	}
	var credential vaults.Credential
	if auth.Type == "static_bearer" {
		if writeFieldError(w, cmp.Or(httpsURL("auth.mcp_server_url", *auth.MCPServerURL), nonEmptyString("auth.token", *auth.Token))) {
			return
		}
		credential, err = h.Vaults.CreateStaticCredential(r.Context(), vaults.CreateStaticCredential{TenantID: tenantID(r), VaultID: vaultID, Name: name, MCPServerURL: *auth.MCPServerURL, Token: *auth.Token, Metadata: request.Metadata})
	} else {
		command, fieldErr := oauthCredentialCreate(auth, name)
		if writeFieldError(w, fieldErr) {
			return
		}
		command.TenantID, command.VaultID, command.Metadata = tenantID(r), vaultID, request.Metadata
		credential, err = h.Vaults.CreateOAuthCredential(r.Context(), command)
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

// rejectEnvironmentVariable rejects the official credential type Core cannot run.
func rejectEnvironmentVariable(authType string) error {
	if authType == "environment_variable" {
		return &fieldError{param: "auth.type", message: "Environment variable credentials are not supported. Use an mcp_oauth or static_bearer credential."}
	}
	return nil
}

func credentialResponse(c vaults.Credential) v1.Credential {
	auth := v1.CredentialAuth{Type: c.AuthType, MCPServerURL: c.MCPServerURL}
	if c.OAuth != nil {
		auth.ExpiresAt = c.OAuth.ExpiresAt
		if r := c.OAuth.Refresh; r != nil {
			auth.Refresh = &v1.OAuthCredentialRefresh{ClientID: r.ClientID, TokenEndpoint: r.TokenEndpoint, TokenEndpointAuth: v1.OAuthEndpointAuth{Type: r.TokenEndpointAuth}, Resource: r.Resource, Scope: r.Scope}
		}
	}
	return v1.Credential{ID: c.ID, VaultID: c.VaultID, Name: c.Name, Object: "vault.credential", Auth: auth, CreatedAt: c.CreatedAt.Unix(), UpdatedAt: c.UpdatedAt.Unix(), Metadata: metadataOrEmpty(c.Metadata)}
}
