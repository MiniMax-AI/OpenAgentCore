package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/echotext"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type MCPCredentialRequest struct {
	ServerLabel, ServerURL string
	CredentialID           *string
}

// MCPCredentialBinding freezes a non-secret selection, including anonymous
// servers. It is private execution configuration, not the public MCP tool shape.
type MCPCredentialBinding struct {
	ServerLabel  string `json:"server_label"`
	ServerURL    string `json:"server_url"`
	VaultID      string `json:"vault_id,omitempty"`
	CredentialID string `json:"credential_id,omitempty"`
	AuthType     string `json:"auth_type,omitempty"`
}

// MCPCredentialSelectionError rejects a Session MCP credential selection with
// the observed official message (MV-03); the API reports a Conflict as 409
// conflict_error and any other as 400 invalid_request_error. Selection searches
// only the attached Vaults, which the caller owns, so a missing, foreign-tenant,
// unattached or malformed reference produces the same error, and only a
// credential of an attached Vault can report a server_url mismatch.
type MCPCredentialSelectionError struct {
	Conflict bool
	Message  string
}

func (e *MCPCredentialSelectionError) Error() string { return e.Message }

// echoed repeats a caller-supplied value in a selection message only within
// the shared bound; otherwise the message leaves it out.
func echoed(value string) string {
	if !echotext.Allowed(value) {
		return ""
	}
	return " " + value
}

func mcpCredentialRequiresVault() error {
	return &MCPCredentialSelectionError{Message: "MCP credential_id requires an attached vault"}
}

func mcpCredentialNotAttached(id string) error {
	return &MCPCredentialSelectionError{Message: "MCP credential_id" + echoed(id) + " was not found in an attached vault"}
}

func mcpCredentialURLMismatch(id, url string) error {
	return &MCPCredentialSelectionError{Message: "MCP credential_id" + echoed(id) + " does not match server_url" + echoed(url)}
}

func mcpCredentialAmbiguous(url string) error {
	return &MCPCredentialSelectionError{Conflict: true, Message: "multiple attached vault credentials match MCP server_url" + echoed(url) + "; specify credential_id"}
}

func attachedVaultIDs(ids []string) ([]pgtype.UUID, error) {
	result := make([]pgtype.UUID, 0, len(ids))
	seen := map[pgtype.UUID]bool{}
	for _, raw := range ids {
		id, err := parseID(raw)
		if err != nil {
			return nil, ErrNotFound
		}
		if !seen[id] {
			result = append(result, id)
			seen[id] = true
		}
	}
	return result, nil
}

// ResolveMCPCredentials reads metadata only. Resource changes after this read do
// not reselect credentials for an accepted Session or its creation retries.
func (s *Store) ResolveMCPCredentials(ctx context.Context, tenantID string, vaultIDs []string, requests []MCPCredentialRequest) ([]MCPCredentialBinding, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return nil, err
	}
	vaults, err := attachedVaultIDs(vaultIDs)
	if err != nil {
		return nil, err
	}
	owned, err := s.queries.GetAttachedVaultIDs(ctx, sqlc.GetAttachedVaultIDsParams{TenantID: tenant, VaultIds: vaults})
	if err != nil {
		return nil, errors.New("cannot resolve attached Vaults")
	}
	if len(owned) != len(vaults) {
		return nil, ErrNotFound
	}
	bindings := make([]MCPCredentialBinding, 0, len(requests))
	for _, request := range requests {
		if request.ServerLabel == "" || request.ServerURL == "" {
			return nil, ErrInvalidInput
		}
		var id pgtype.UUID
		if request.CredentialID != nil {
			if len(vaults) == 0 {
				return nil, mcpCredentialRequiresVault()
			}
			id, err = parseID(*request.CredentialID)
			if err != nil {
				return nil, mcpCredentialNotAttached(*request.CredentialID)
			}
		}
		rows, err := s.queries.FindMCPCredentials(ctx, sqlc.FindMCPCredentialsParams{
			TenantID: tenant, VaultIds: vaults, McpServerUrl: request.ServerURL, CredentialID: id,
		})
		if err != nil {
			return nil, errors.New("cannot resolve MCP credential")
		}
		if request.CredentialID != nil {
			if len(rows) == 0 {
				return nil, mcpCredentialNotAttached(*request.CredentialID)
			}
			if rows[0].McpServerUrl != request.ServerURL {
				return nil, mcpCredentialURLMismatch(*request.CredentialID, request.ServerURL)
			}
		}
		if len(rows) > 1 {
			return nil, mcpCredentialAmbiguous(request.ServerURL)
		}
		binding := MCPCredentialBinding{ServerLabel: request.ServerLabel, ServerURL: request.ServerURL}
		if len(rows) == 1 {
			binding.VaultID, binding.CredentialID = uuid.UUID(rows[0].VaultID.Bytes).String(), uuid.UUID(rows[0].ID.Bytes).String()
			binding.AuthType = rows[0].AuthType
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

// MCPBearerToken is execution-only: recheck the complete frozen authorization
// before decrypting. Never persist or log its result, or downgrade failure to an
// anonymous request. Public resource queries do not select ciphertext.
func (s *Store) MCPBearerToken(ctx context.Context, tenantID string, vaultIDs []string, binding MCPCredentialBinding) (string, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return "", ErrNotFound
	}
	vaults, err := attachedVaultIDs(vaultIDs)
	if err != nil {
		return "", err
	}
	vault, err := parseID(binding.VaultID)
	if err != nil {
		return "", ErrNotFound
	}
	id, err := parseID(binding.CredentialID)
	if err != nil || (binding.AuthType != "static_bearer" && binding.AuthType != "mcp_oauth") || binding.ServerURL == "" {
		return "", ErrNotFound
	}
	if binding.AuthType == "mcp_oauth" {
		attached := false
		for _, candidate := range vaults {
			if candidate == vault {
				attached = true
			}
		}
		if !attached {
			return "", ErrNotFound
		}
		return s.oauthBearerToken(ctx, tenantID, binding)
	}
	ciphertext, err := s.queries.GetMCPStaticCredentialCiphertext(ctx, sqlc.GetMCPStaticCredentialCiphertextParams{
		TenantID: tenant, VaultIds: vaults, VaultID: vault, CredentialID: id, McpServerUrl: binding.ServerURL,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", errors.New("cannot read MCP credential")
	}
	if s.credentialCipher == nil {
		return "", ErrCredentialStorageUnavailable
	}
	plaintext, err := s.credentialCipher.Open(ciphertext, credentialcrypto.Binding{
		TenantID: uuid.UUID(tenant.Bytes).String(), VaultID: uuid.UUID(vault.Bytes).String(), CredentialID: uuid.UUID(id.Bytes).String(),
		AuthType: binding.AuthType, Destination: binding.ServerURL,
	})
	if err != nil {
		return "", errors.New("MCP credential decryption failed")
	}
	return string(plaintext), nil
}
