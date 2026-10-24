package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/metadata"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

// Vaults runs the Vault and Credential use cases, and selects the Credentials
// a Session's MCP servers use at creation.
type Vaults interface {
	CreateVault(context.Context, vaults.CreateVault) (vaults.Vault, error)
	DeleteVault(context.Context, vaults.DeleteVault) (string, error)
	CreateStaticCredential(context.Context, vaults.CreateStaticCredential) (vaults.Credential, error)
	UpdateStaticCredential(context.Context, vaults.UpdateStaticCredential) (vaults.Credential, error)
	CreateOAuthCredential(context.Context, vaults.CreateOAuthCredential) (vaults.Credential, error)
	UpdateOAuthCredential(context.Context, vaults.UpdateOAuthCredential) (vaults.Credential, error)
	DeleteCredential(context.Context, vaults.DeleteCredential) (string, error)
	ResolveMCPCredentials(context.Context, vaults.ResolveMCPCredentials) ([]vaults.MCPCredentialBinding, error)
}

// VaultsReader reads Vaults and the public metadata of their Credentials.
type VaultsReader interface {
	GetVault(ctx context.Context, tenantID, vaultID string) (vaults.Vault, error)
	ListVaults(ctx context.Context, tenantID string, query vaults.PageQuery) (vaults.VaultPage, error)
	GetCredential(ctx context.Context, tenantID, vaultID, credentialID string) (vaults.Credential, error)
	ListCredentials(ctx context.Context, tenantID, vaultID string, query vaults.PageQuery) (vaults.CredentialPage, error)
}

func (h *Handler) createVault(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONObject(w, r)
	if !ok || writeFieldError(w, validateBody(raw, createVaultParams)) {
		return
	}
	var request struct {
		Name     *string           `json:"name"`
		Metadata map[string]string `json:"metadata"`
	}
	_ = json.Unmarshal(raw, &request)
	input := vaults.CreateVault{TenantID: tenantID(r)}
	if request.Name != nil {
		name, err := normalizedVaultName(*request.Name)
		if err != nil {
			writeFieldError(w, err)
			return
		}
		input.Name = &name
	}
	// Vault metadata has no pair or character limits, only the storage limits.
	input.Metadata = metadataOrEmpty(request.Metadata)
	if writeFieldError(w, metadataFieldError(metadata.ValidateStorable(input.Metadata))) {
		return
	}
	vault, err := h.Vaults.CreateVault(r.Context(), input)
	if err != nil {
		writeVaultsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, vaultResponse(vault))
}

func (h *Handler) getVault(w http.ResponseWriter, r *http.Request) {
	id, ok := credentialResourceID(w, r, "vault_id")
	if !ok {
		return
	}
	vault, err := h.VaultsReader.GetVault(r.Context(), tenantID(r), id)
	if err != nil {
		writeVaultsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, vaultResponse(vault))
}

func vaultResponse(vault vaults.Vault) v1.Vault {
	return v1.Vault{ID: vault.ID, Object: "vault", CreatedAt: vault.CreatedAt.Unix(), Name: vault.Name, Metadata: vault.Metadata}
}

func normalizedVaultName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if len(name) == 0 || len(name) > 256 {
		return "", &fieldError{param: "name", message: "name must contain 1 to 256 UTF-8 bytes after trimming."}
	}
	return name, nil
}
