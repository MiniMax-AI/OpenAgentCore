package vaults

// MCPCredentialRequest is one MCP server of a Session being created, with the
// Credential the caller named for it, if any.
type MCPCredentialRequest struct {
	ServerLabel, ServerURL string
	CredentialID           *string
}

// MCPCredentialBinding freezes a non-secret selection, including anonymous
// servers. It is private execution configuration stored in the Session, not
// the public MCP tool shape, so its JSON names are stable.
type MCPCredentialBinding struct {
	ServerLabel  string `json:"server_label"`
	ServerURL    string `json:"server_url"`
	VaultID      string `json:"vault_id,omitempty"`
	CredentialID string `json:"credential_id,omitempty"`
	AuthType     string `json:"auth_type,omitempty"`
}

// MCPCredentialMatch is the non-secret identity of a Credential that an MCP
// credential lookup found.
type MCPCredentialMatch struct {
	VaultID, CredentialID, AuthType, MCPServerURL string
}

// attachedVaultIDs returns the canonical, deduplicated IDs of a Session's
// attached Vaults. An ID that cannot name a Vault is ErrNotFound.
func attachedVaultIDs(ids []string) ([]string, error) {
	result := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, raw := range ids {
		id, ok := canonicalID(raw)
		if !ok {
			return nil, ErrNotFound
		}
		if !seen[id] {
			result = append(result, id)
			seen[id] = true
		}
	}
	return result, nil
}

// mcpCredentialLookup checks a request before its lookup and returns the
// canonical Credential ID the caller named, or "" to select by destination.
func mcpCredentialLookup(request MCPCredentialRequest, attached []string) (string, error) {
	if request.ServerLabel == "" || request.ServerURL == "" {
		return "", ErrInvalidInput
	}
	if request.CredentialID == nil {
		return "", nil
	}
	if len(attached) == 0 {
		return "", mcpCredentialRequiresVault()
	}
	id, ok := canonicalID(*request.CredentialID)
	if !ok {
		return "", mcpCredentialNotAttached(*request.CredentialID)
	}
	return id, nil
}

// selectMCPCredential binds a request to the Credentials its lookup found: a
// named Credential must exist in an attached Vault and match the destination,
// and a destination may match at most one Credential.
func selectMCPCredential(request MCPCredentialRequest, matches []MCPCredentialMatch) (MCPCredentialBinding, error) {
	if request.CredentialID != nil {
		if len(matches) == 0 {
			return MCPCredentialBinding{}, mcpCredentialNotAttached(*request.CredentialID)
		}
		if matches[0].MCPServerURL != request.ServerURL {
			return MCPCredentialBinding{}, mcpCredentialURLMismatch(*request.CredentialID, request.ServerURL)
		}
	}
	if len(matches) > 1 {
		return MCPCredentialBinding{}, mcpCredentialAmbiguous(request.ServerURL)
	}
	binding := MCPCredentialBinding{ServerLabel: request.ServerLabel, ServerURL: request.ServerURL}
	if len(matches) == 1 {
		binding.VaultID, binding.CredentialID, binding.AuthType = matches[0].VaultID, matches[0].CredentialID, matches[0].AuthType
	}
	return binding, nil
}

// bearerScope is a frozen binding's complete authorization, in canonical IDs.
type bearerScope struct {
	tenantID, vaultID, credentialID string
	attached                        []string
}

// bearerTokenScope rechecks a frozen binding before any lookup. Every failure
// is ErrNotFound: execution never downgrades to an anonymous request.
func bearerTokenScope(command MCPBearerToken) (bearerScope, error) {
	tenant, ok := canonicalID(command.TenantID)
	if !ok {
		return bearerScope{}, ErrNotFound
	}
	attached, err := attachedVaultIDs(command.VaultIDs)
	if err != nil {
		return bearerScope{}, err
	}
	binding := command.Binding
	vault, ok := canonicalID(binding.VaultID)
	if !ok {
		return bearerScope{}, ErrNotFound
	}
	id, ok := canonicalID(binding.CredentialID)
	if !ok || (binding.AuthType != AuthStaticBearer && binding.AuthType != AuthMCPOAuth) || binding.ServerURL == "" {
		return bearerScope{}, ErrNotFound
	}
	return bearerScope{tenantID: tenant, vaultID: vault, credentialID: id, attached: attached}, nil
}

func (s bearerScope) vaultAttached() bool {
	for _, candidate := range s.attached {
		if candidate == s.vaultID {
			return true
		}
	}
	return false
}
