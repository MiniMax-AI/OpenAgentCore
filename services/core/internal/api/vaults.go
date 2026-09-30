package api

import (
	"context"
	"encoding/json"
	"errors"
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

// @Summary Create a Vault
// @Description Creates a project-owned Vault independently of execution. Omitted name stays null; a supplied string is trimmed and must contain 1–256 UTF-8 bytes. Explicit null name is invalid. Omitted/null metadata becomes an empty object; non-string values return invalid_request_error with a metadata.<key> param. Metadata has a local 64 KiB encoded storage bound. U+0000 in stored strings is rejected as a local storage limit. Credentials, Session binding and hosted error/retry parity remain incomplete.
// @Tags Vaults
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param body body v1.CreateVaultRequest true "Vault name and metadata"
// @Success 201 {object} v1.Vault
// @Failure 400,401,413,500 {object} v1.ErrorResponse
// @Router /vaults [post]
func (h *Handler) createVault(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONObject(w, r)
	if !ok {
		return
	}
	if writeFieldError(w, metadataTypeError(raw)) {
		return
	}
	var request struct {
		Name     json.RawMessage    `json:"name"`
		Metadata map[string]*string `json:"metadata"`
	}
	if decodeInputObject(raw, &request, "name", "metadata") != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request must be a JSON object containing supported fields.")
		return
	}
	input := vaults.CreateVault{TenantID: tenantID(r)}
	if len(request.Name) > 0 {
		var name *string
		if json.Unmarshal(request.Name, &name) != nil || name == nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "name must be a string.")
			return
		}
		trimmed, err := normalizedVaultName(*name)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		input.Name = &trimmed
	}
	// Vault metadata has no pair or character limits, only the storage limits.
	var err error
	input.Metadata, err = stringMetadata(request.Metadata)
	if err == nil {
		err = metadataFieldError(metadata.ValidateStorable(input.Metadata))
	}
	if err != nil {
		if !writeFieldError(w, err) {
			writeError(w, http.StatusBadRequest, "invalid_request", "metadata values must be strings.")
		}
		return
	}
	vault, err := h.Vaults.CreateVault(r.Context(), input)
	if err != nil {
		writeVaultsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, vaultResponse(vault))
}

// @Summary Retrieve a Vault
// @Description Reads a Vault owned by the authenticated project without resolving credentials, Sessions or execution devices. Missing and foreign IDs share the same not-found response; exact hosted error semantics remain unverified.
// @Tags Vaults
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param vault_id path string true "Vault ID"
// @Success 200 {object} v1.Vault
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /vaults/{vault_id} [get]
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
		return "", errors.New("name must contain 1 to 256 UTF-8 bytes after trimming.")
	}
	return name, nil
}
