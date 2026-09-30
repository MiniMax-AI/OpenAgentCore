package vaults

import "context"

// Reader reads Vaults and Credential metadata within one tenant. A malformed
// tenant, Vault or Credential ID is ErrInvalidInput, except the parent Vault
// of ListCredentials, which is ErrNotFound; a missing or foreign resource is
// ErrNotFound.
type Reader interface {
	GetVault(ctx context.Context, tenantID, vaultID string) (Vault, error)
	ListVaults(ctx context.Context, tenantID string, query PageQuery) (VaultPage, error)
	GetCredential(ctx context.Context, tenantID, vaultID, credentialID string) (Credential, error)
	// ListCredentials resolves the parent Vault first: an inaccessible parent
	// is ErrNotFound, never an empty page.
	ListCredentials(ctx context.Context, tenantID, vaultID string, query PageQuery) (CredentialPage, error)
}

// Storage persists Vaults and Credentials. Every operation is scoped to the
// tenant it names, and every caller-visible write records its audit row in
// the same transaction. In a write, a malformed ID of an existing resource
// names none and is ErrNotFound; a malformed tenant of a new Vault or a
// malformed new Credential ID is ErrInvalidInput.
type Storage interface {
	Reader

	// CreateVault stores a new Vault and allocates its ID.
	CreateVault(ctx context.Context, vault NewVault) (Vault, error)
	// DeleteVault deletes a Vault with all of its Credentials and returns its ID.
	DeleteVault(ctx context.Context, tenantID, vaultID string) (string, error)
	// CreateCredential seals the Credential's secret and stores it in a Vault
	// of the tenant. A missing credential key is
	// credentialcrypto.ErrUnavailable, checked after the new Credential ID and
	// before the Vault.
	CreateCredential(ctx context.Context, credential NewCredential) (Credential, error)
	// ReplaceStaticToken seals and replaces a static_bearer Credential's
	// token. Without a credential key it is credentialcrypto.ErrUnavailable.
	ReplaceStaticToken(ctx context.Context, replacement StaticTokenReplacement) (Credential, error)
	// DeleteCredential deletes a Credential and its sealed secret and returns its ID.
	DeleteCredential(ctx context.Context, key CredentialKey) (string, error)

	// WithOAuthCredential runs apply in one transaction and commits only when
	// apply returns nil. It returns apply's error unchanged.
	WithOAuthCredential(ctx context.Context, key CredentialKey, apply func(OAuthTx) error) error

	// CountOwnedVaults counts how many of the given Vaults the tenant owns.
	CountOwnedVaults(ctx context.Context, tenantID string, vaultIDs []string) (int, error)
	// FindMCPCredentials returns at most two Credentials of the attached
	// Vaults that the query selects, ordered by ID.
	FindMCPCredentials(ctx context.Context, query MCPCredentialQuery) ([]MCPCredentialMatch, error)
	// StaticToken opens a static_bearer Credential's token when the complete
	// frozen scope still names it. A scope that names none is ErrNotFound,
	// then a missing credential key is credentialcrypto.ErrUnavailable.
	StaticToken(ctx context.Context, query StaticTokenQuery) (string, error)
}

// OAuthTx is one mcp_oauth Credential inside a WithOAuthCredential
// transaction.
type OAuthTx interface {
	// LoadOAuthGrant locks the Credential until the transaction ends, so
	// competing refreshes, replacements and deletions, including the parent
	// Vault's, wait. A non-empty destination must match the stored one, or
	// the Credential is ErrNotFound. Only then is the grant opened: a missing
	// credential key is credentialcrypto.ErrUnavailable, and stored metadata
	// that differs from the sealed copy fails authentication.
	LoadOAuthGrant(ctx context.Context, destination string) (OAuthGrant, error)
	// ApplyOAuthRefresh seals and stores a refreshed grant for the loaded
	// Credential. Execution refreshes are not caller writes and record no
	// audit row.
	ApplyOAuthRefresh(ctx context.Context, grant OAuthGrant) error
	// ApplyOAuthReplacement seals and stores a caller's replacement for the
	// loaded Credential and audits it.
	ApplyOAuthReplacement(ctx context.Context, grant OAuthGrant) (Credential, error)
}

// CredentialKey names one Credential of one Vault of one tenant.
type CredentialKey struct {
	TenantID, VaultID, CredentialID string
}

// NewVault is a Vault ready to store. Metadata is its encoded JSON object.
type NewVault struct {
	TenantID string
	Name     *string
	Metadata []byte
}

// NewCredential is a Credential with its new ID, ready to store. Storage
// seals its secret to the tenant, the Vault, that ID, the authentication type
// and the destination: Token for static_bearer, OAuth for mcp_oauth.
type NewCredential struct {
	CredentialKey
	Name, AuthType, MCPServerURL string
	Token                        string
	OAuth                        OAuthGrant
}

// StaticTokenReplacement is a new static_bearer token. The write matches the
// destination the token is sealed to.
type StaticTokenReplacement struct {
	CredentialKey
	MCPServerURL string
	Token        string
}

// MCPCredentialQuery selects a named Credential by ID alone, so its
// destination can be compared, and otherwise selects by exact destination.
type MCPCredentialQuery struct {
	TenantID     string
	VaultIDs     []string
	ServerURL    string
	CredentialID string
}

// StaticTokenQuery is a frozen binding's complete scope.
type StaticTokenQuery struct {
	TenantID                            string
	VaultIDs                            []string
	VaultID, CredentialID, MCPServerURL string
}
