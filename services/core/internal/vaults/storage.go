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
	// CreateCredential stores a sealed Credential in a Vault of the tenant.
	CreateCredential(ctx context.Context, credential NewCredential) (Credential, error)
	// ReplaceStaticToken replaces a static_bearer Credential's sealed token.
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
	// StaticTokenCiphertext returns a static_bearer Credential's sealed token
	// when the complete frozen scope still names it.
	StaticTokenCiphertext(ctx context.Context, query StaticTokenQuery) ([]byte, error)
}

// OAuthTx is one mcp_oauth Credential inside a WithOAuthCredential
// transaction.
type OAuthTx interface {
	// LoadOAuthCredential locks the Credential until the transaction ends, so
	// competing refreshes, replacements and deletions, including the parent
	// Vault's, wait. It returns the metadata and the sealed secret.
	LoadOAuthCredential(ctx context.Context) (Credential, []byte, error)
	// ApplyOAuthRefresh stores a refreshed grant. Execution refreshes are not
	// caller writes and record no audit row.
	ApplyOAuthRefresh(ctx context.Context, sealed SealedOAuth) error
	// ApplyOAuthReplacement stores a caller's replacement and audits it.
	ApplyOAuthReplacement(ctx context.Context, sealed SealedOAuth) (Credential, error)
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

// NewCredential is a Credential with its ID, sealed to that ID, ready to store.
type NewCredential struct {
	CredentialKey
	Name, AuthType, MCPServerURL string
	// OAuthMetadata is the encoded OAuthMetadata of an mcp_oauth Credential.
	OAuthMetadata []byte
	Ciphertext    []byte
}

// StaticTokenReplacement is a static_bearer token sealed to the destination
// the write matches.
type StaticTokenReplacement struct {
	CredentialKey
	MCPServerURL string
	Ciphertext   []byte
}

// SealedOAuth is an OAuth grant sealed to the destination the write matches,
// with the metadata the seal authenticates.
type SealedOAuth struct {
	MCPServerURL         string
	Metadata, Ciphertext []byte
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
