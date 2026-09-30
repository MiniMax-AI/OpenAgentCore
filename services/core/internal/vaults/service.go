package vaults

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/metadata"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/oauthrefresh"
)

// oauthRefreshTimeout bounds both lock contention and the external exchange,
// so a slow provider cannot hold a Credential indefinitely. The refresh client
// has its own tighter bound.
const oauthRefreshTimeout = 20 * time.Second

// Service runs the Vault and Credential operations. Storage seals and opens
// the secrets; without a credential key, operations that need a secret return
// credentialcrypto.ErrUnavailable and the others keep working.
type Service struct {
	storage   Storage
	refresher oauthrefresh.Refresher
}

// NewService requires storage and the OAuth refresher.
func NewService(storage Storage, refresher oauthrefresh.Refresher) (*Service, error) {
	if storage == nil {
		return nil, errors.New("vaults: storage is required")
	}
	if refresher == nil {
		return nil, errors.New("vaults: OAuth refresher is required")
	}
	return &Service{storage: storage, refresher: refresher}, nil
}

type CreateVault struct {
	TenantID string
	// Name is the public layer's trimmed name, or nil for none.
	Name     *string
	Metadata map[string]string
}

// CreateVault stores a new Vault. Each call creates a distinct Vault.
func (s *Service) CreateVault(ctx context.Context, command CreateVault) (Vault, error) {
	if command.Name != nil && !validName(*command.Name) {
		return Vault{}, fmt.Errorf("%w: vault name must contain 1–256 UTF-8 bytes", ErrInvalidInput)
	}
	encoded, err := metadata.Encode(command.Metadata)
	if err != nil {
		return Vault{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	return s.storage.CreateVault(ctx, NewVault{TenantID: command.TenantID, Name: command.Name, Metadata: encoded})
}

type DeleteVault struct{ TenantID, VaultID string }

// DeleteVault deletes a Vault with its Credentials. Sessions keep their frozen
// bindings, which then resolve to ErrNotFound.
func (s *Service) DeleteVault(ctx context.Context, command DeleteVault) (string, error) {
	return s.storage.DeleteVault(ctx, command.TenantID, command.VaultID)
}

type CreateStaticCredential struct {
	TenantID, VaultID         string
	Name, MCPServerURL, Token string
}

// CreateStaticCredential stores a bearer token under a new Credential ID.
// Storage seals it to its tenant, Vault, that ID and destination.
func (s *Service) CreateStaticCredential(ctx context.Context, command CreateStaticCredential) (Credential, error) {
	tenant, ok := canonicalID(command.TenantID)
	if !ok {
		return Credential{}, ErrInvalidInput
	}
	if !validName(command.Name) || command.MCPServerURL == "" {
		return Credential{}, ErrInvalidInput
	}
	// Storage reports a missing credential key before a malformed or missing
	// Vault.
	key := CredentialKey{TenantID: tenant, VaultID: command.VaultID, CredentialID: uuid.NewString()}
	return s.storage.CreateCredential(ctx, NewCredential{CredentialKey: key, Name: command.Name,
		AuthType: AuthStaticBearer, MCPServerURL: command.MCPServerURL, Token: command.Token})
}

type UpdateStaticCredential struct {
	TenantID, VaultID, CredentialID string
	Token                           string
}

// UpdateStaticCredential replaces only the token and the update time. The
// stored destination is the seal's scope, which the write checks again, so
// the existing frozen bindings read the replacement.
func (s *Service) UpdateStaticCredential(ctx context.Context, command UpdateStaticCredential) (Credential, error) {
	key, ok := credentialKey(command.TenantID, command.VaultID, command.CredentialID)
	if !ok {
		return Credential{}, ErrNotFound
	}
	current, err := s.storage.GetCredential(ctx, key.TenantID, key.VaultID, key.CredentialID)
	if err != nil {
		return Credential{}, err
	}
	if current.AuthType != AuthStaticBearer {
		return Credential{}, ErrInvalidInput
	}
	return s.storage.ReplaceStaticToken(ctx, StaticTokenReplacement{CredentialKey: key, MCPServerURL: current.MCPServerURL, Token: command.Token})
}

// CreateOAuthCredential keeps the write-only secrets apart from the metadata.
type CreateOAuthCredential struct {
	TenantID, VaultID               string
	Name, MCPServerURL, AccessToken string
	OAuth                           OAuthMetadata
	RefreshToken, ClientSecret      string
}

// CreateOAuthCredential stores a grant, with the metadata it is refreshed by,
// under a new Credential ID. Storage seals it to its tenant, Vault, that ID
// and destination.
func (s *Service) CreateOAuthCredential(ctx context.Context, command CreateOAuthCredential) (Credential, error) {
	tenant, ok := canonicalID(command.TenantID)
	if !ok {
		return Credential{}, ErrNotFound
	}
	if !validOAuthCreation(command) {
		return Credential{}, ErrInvalidInput
	}
	// Storage reports a missing credential key before a malformed or missing
	// Vault.
	key := CredentialKey{TenantID: tenant, VaultID: command.VaultID, CredentialID: uuid.NewString()}
	return s.storage.CreateCredential(ctx, NewCredential{CredentialKey: key, Name: command.Name, AuthType: AuthMCPOAuth, MCPServerURL: command.MCPServerURL,
		OAuth: OAuthGrant{Metadata: command.OAuth, AccessToken: command.AccessToken, RefreshToken: command.RefreshToken, ClientSecret: command.ClientSecret}})
}

// UpdateOAuthCredential patches an OAuth grant. ExpiresAtSet keeps omitted
// apart from null; a nil AccessToken keeps the stored one.
type UpdateOAuthCredential struct {
	TenantID, VaultID, CredentialID string
	AccessToken                     *string
	ExpiresAt                       *string
	ExpiresAtSet                    bool
	Refresh                         *OAuthRefreshUpdate
}

// UpdateOAuthCredential applies the patch to the locked grant, so it
// serializes with refreshes.
func (s *Service) UpdateOAuthCredential(ctx context.Context, command UpdateOAuthCredential) (Credential, error) {
	key, ok := credentialKey(command.TenantID, command.VaultID, command.CredentialID)
	if !ok {
		return Credential{}, ErrNotFound
	}
	current, err := s.storage.GetCredential(ctx, key.TenantID, key.VaultID, key.CredentialID)
	if err != nil {
		return Credential{}, err
	}
	if current.AuthType != AuthMCPOAuth {
		return Credential{}, ErrInvalidInput
	}
	var updated Credential
	err = s.withOAuth(ctx, key, "", "credential update failed", func(tx OAuthTx, grant OAuthGrant) error {
		grant, err := applyOAuthUpdate(grant, command)
		if err != nil {
			return err
		}
		if !validOAuthMetadata(grant.Metadata) {
			return ErrInvalidInput
		}
		updated, err = tx.ApplyOAuthReplacement(ctx, grant)
		return err
	})
	if err != nil {
		return Credential{}, err
	}
	return updated, nil
}

type DeleteCredential struct{ TenantID, VaultID, CredentialID string }

// DeleteCredential removes the Credential without reading or opening its secret.
func (s *Service) DeleteCredential(ctx context.Context, command DeleteCredential) (string, error) {
	return s.storage.DeleteCredential(ctx, CredentialKey{TenantID: command.TenantID, VaultID: command.VaultID, CredentialID: command.CredentialID})
}

type ResolveMCPCredentials struct {
	TenantID string
	VaultIDs []string
	Requests []MCPCredentialRequest
}

// ResolveMCPCredentials selects each MCP server's Credential from the attached
// Vaults, reading metadata only. Later resource changes do not reselect
// Credentials for an accepted Session or its creation retries.
func (s *Service) ResolveMCPCredentials(ctx context.Context, command ResolveMCPCredentials) ([]MCPCredentialBinding, error) {
	tenant, ok := canonicalID(command.TenantID)
	if !ok {
		return nil, ErrInvalidInput
	}
	attached, err := attachedVaultIDs(command.VaultIDs)
	if err != nil {
		return nil, err
	}
	owned, err := s.storage.CountOwnedVaults(ctx, tenant, attached)
	if err != nil {
		return nil, err
	}
	if owned != len(attached) {
		return nil, ErrNotFound
	}
	bindings := make([]MCPCredentialBinding, 0, len(command.Requests))
	for _, request := range command.Requests {
		id, err := mcpCredentialLookup(request, attached)
		if err != nil {
			return nil, err
		}
		matches, err := s.storage.FindMCPCredentials(ctx, MCPCredentialQuery{TenantID: tenant, VaultIDs: attached, ServerURL: request.ServerURL, CredentialID: id})
		if err != nil {
			return nil, err
		}
		binding, err := selectMCPCredential(request, matches)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

// MCPBearerToken asks for the bearer token of a Session's frozen binding.
type MCPBearerToken struct {
	TenantID string
	VaultIDs []string
	Binding  MCPCredentialBinding
}

// MCPBearerToken is for execution only. It rechecks the complete frozen
// authorization before opening a secret, and refreshes an expired OAuth grant
// while holding its lock. Never persist or log the token, or downgrade a
// failure to an anonymous request.
func (s *Service) MCPBearerToken(ctx context.Context, command MCPBearerToken) (string, error) {
	scope, err := bearerTokenScope(command)
	if err != nil {
		return "", err
	}
	if command.Binding.AuthType == AuthMCPOAuth {
		if !scope.vaultAttached() {
			return "", ErrNotFound
		}
		return s.oauthBearerToken(ctx, CredentialKey{TenantID: scope.tenantID, VaultID: scope.vaultID, CredentialID: scope.credentialID}, command.Binding.ServerURL)
	}
	return s.storage.StaticToken(ctx, StaticTokenQuery{TenantID: scope.tenantID, VaultIDs: scope.attached,
		VaultID: scope.vaultID, CredentialID: scope.credentialID, MCPServerURL: command.Binding.ServerURL})
}

func (s *Service) oauthBearerToken(ctx context.Context, key CredentialKey, destination string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, oauthRefreshTimeout)
	defer cancel()
	var bearer string
	err := s.withOAuth(ctx, key, destination, "OAuth credential refresh commit failed", func(tx OAuthTx, grant OAuthGrant) error {
		token, expired, err := currentAccessToken(grant, time.Now())
		if err != nil || !expired {
			bearer = token
			return err
		}
		request, err := refreshRequest(grant)
		if err != nil {
			return err
		}
		refreshed, err := s.refresher.Refresh(ctx, request)
		if err != nil {
			return errors.New("OAuth credential refresh failed")
		}
		grant, err = applyRefreshedToken(grant, refreshed, time.Now())
		if err != nil {
			return err
		}
		if !validOAuthMetadata(grant.Metadata) {
			return ErrInvalidInput
		}
		if err := tx.ApplyOAuthRefresh(ctx, grant); err != nil {
			return err
		}
		bearer = grant.AccessToken
		return nil
	})
	if err != nil {
		return "", err
	}
	return bearer, nil
}

// withOAuth locks the Credential for the whole of apply, including an
// external refresh, and commits only when apply succeeds. PostgreSQL
// serializes competing updates and deletions, including the parent Vault's
// cascade. A non-empty destination must match the stored one. The opened
// grant's metadata must still be valid, so a changed stored setting never
// reaches a provider. A failure to begin or commit is reported as failure,
// never with database error text.
func (s *Service) withOAuth(ctx context.Context, key CredentialKey, destination, failure string, apply func(OAuthTx, OAuthGrant) error) error {
	var applied error
	err := s.storage.WithOAuthCredential(ctx, key, func(tx OAuthTx) error {
		grant, err := tx.LoadOAuthGrant(ctx, destination)
		if err == nil && !validOAuthMetadata(grant.Metadata) {
			err = errors.New("OAuth credential authentication failed")
		}
		if err == nil {
			err = apply(tx, grant)
		}
		applied = err
		return err
	})
	if err != nil && applied == nil {
		return errors.New(failure)
	}
	return err
}

// credentialKey canonicalizes a Credential's IDs.
func credentialKey(tenantID, vaultID, credentialID string) (CredentialKey, bool) {
	tenant, ok1 := canonicalID(tenantID)
	vault, ok2 := canonicalID(vaultID)
	id, ok3 := canonicalID(credentialID)
	return CredentialKey{TenantID: tenant, VaultID: vault, CredentialID: id}, ok1 && ok2 && ok3
}
