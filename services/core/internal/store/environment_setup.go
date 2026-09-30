package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) sealEnvironmentSetup(tenant, resource, id, field string, input any, empty bool) ([]byte, error) {
	if empty {
		return nil, nil
	}
	plaintext, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return s.credentialCipher.SealEnvironmentSetup(plaintext, credentialcrypto.EnvironmentSetupBinding{TenantID: tenant, Resource: resource, OwnerID: id, Field: field})
}

func (s *Store) openEnvironmentSetup(tenant, resource, id, field string, ciphertext []byte, output any) error {
	if len(ciphertext) == 0 {
		return nil
	}
	plaintext, err := s.credentialCipher.OpenEnvironmentSetup(ciphertext, credentialcrypto.EnvironmentSetupBinding{TenantID: tenant, Resource: resource, OwnerID: id, Field: field})
	if err != nil {
		return err
	}
	if environmentconfig.Decode(plaintext, output) != nil {
		return ErrInvalidInput
	}
	return nil
}

func (s *Store) saveEnvironmentSetup(ctx context.Context, q *sqlc.Queries, tenant string, session pgtype.UUID, setup environmentconfig.Setup) error {
	if setup.ValidateInstalled() != nil {
		return ErrInvalidInput
	}
	if setup.Empty() {
		return nil
	}
	owner, err := parseID(tenant)
	if err != nil {
		return err
	}
	encrypted, err := s.sealEnvironmentSetup(uuid.UUID(owner.Bytes).String(), "session", uuid.UUID(session.Bytes).String(), "initialization", setup, false)
	if err != nil {
		return err
	}
	return q.CreateEnvironmentSetup(ctx, sqlc.CreateEnvironmentSetupParams{SessionID: session, Contents: encrypted})
}

func (s *Store) ReadEnvironmentSetup(ctx context.Context, tenant, session string) (environmentconfig.Setup, error) {
	var result environmentconfig.Setup
	lookup, err := deviceLookup(tenant, session)
	if err != nil {
		return result, ErrNotFound
	}
	encrypted, err := s.queries.GetEnvironmentSetup(ctx, sqlc.GetEnvironmentSetupParams{TenantID: lookup.TenantID, ID: lookup.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if err = s.openEnvironmentSetup(uuid.UUID(lookup.TenantID.Bytes).String(), "session", uuid.UUID(lookup.ID.Bytes).String(), "initialization", encrypted, &result); err != nil {
		return result, err
	}
	if result.ValidateInstalled() != nil {
		return result, ErrInvalidInput
	}
	return result, nil
}

func (s *Store) sealTemplateSetup(tenant, id string, setup environmentconfig.Setup) ([]byte, []byte, []byte, error) {
	packages, err := json.Marshal(setup.PackageMetadata())
	if err != nil {
		return nil, nil, nil, err
	}
	env, err := s.sealEnvironmentSetup(tenant, "environment_template", id, "env", setup.Env, len(setup.Env) == 0)
	if err != nil {
		return nil, nil, nil, err
	}
	commands, err := s.sealEnvironmentSetup(tenant, "environment_template", id, "setup_commands", setup.Commands, len(setup.Commands) == 0)
	return packages, env, commands, err
}
