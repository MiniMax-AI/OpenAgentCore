package templatepg

import (
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmenttemplates"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// sealed is an Input's column values. Empty confidential fields are stored as
// NULL without using the key, so Templates without them need none; any other
// confidential field without a key fails with credentialcrypto.ErrUnavailable.
type sealed struct {
	files, fileContents     []byte
	packages, env, commands []byte
	skills, skillContents   []byte
	plugins, pluginContents []byte
}

func (s *Store) seal(tenant, id pgtype.UUID, in environmenttemplates.Input) (sealed, error) {
	var out sealed
	var err error
	if out.files, err = json.Marshal(environmentconfig.InitialFilesMetadata(in.Files)); err != nil {
		return out, err
	}
	if len(in.Files) > 0 {
		if s.cipher == nil {
			return out, credentialcrypto.ErrUnavailable
		}
		plaintext, err := json.Marshal(in.Files)
		if err != nil {
			return out, err
		}
		if out.fileContents, err = s.cipher.SealEnvironmentFile(plaintext, fileBinding(tenant, id)); err != nil {
			return out, err
		}
	}
	if out.packages, err = json.Marshal(in.Setup.PackageMetadata()); err != nil {
		return out, err
	}
	if out.skills, err = json.Marshal(in.Setup.SkillMetadata()); err != nil {
		return out, err
	}
	if out.plugins, err = json.Marshal(in.Setup.PluginMetadata()); err != nil {
		return out, err
	}
	for _, field := range []struct {
		name   string
		value  any
		empty  bool
		output *[]byte
	}{
		{"env", in.Setup.Env, len(in.Setup.Env) == 0, &out.env},
		{"setup_commands", in.Setup.Commands, len(in.Setup.Commands) == 0, &out.commands},
		{"skills", in.Setup.Skills, len(in.Setup.Skills) == 0, &out.skillContents},
		{"plugins", in.Setup.Plugins, len(in.Setup.Plugins) == 0, &out.pluginContents},
	} {
		if field.empty {
			continue
		}
		if s.cipher == nil {
			return out, credentialcrypto.ErrUnavailable
		}
		plaintext, err := json.Marshal(field.value)
		if err != nil {
			return out, err
		}
		if *field.output, err = s.cipher.SealEnvironmentSetup(plaintext, setupBinding(tenant, id, field.name)); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (s *Store) openSetup(tenant, id pgtype.UUID, field string, ciphertext []byte, output any) error {
	if len(ciphertext) == 0 {
		return nil
	}
	if s.cipher == nil {
		return credentialcrypto.ErrUnavailable
	}
	plaintext, err := s.cipher.OpenEnvironmentSetup(ciphertext, setupBinding(tenant, id, field))
	if err != nil {
		return err
	}
	if environmentconfig.Decode(plaintext, output) != nil {
		return environmenttemplates.ErrInvalidInput
	}
	return nil
}

func setupBinding(tenant, id pgtype.UUID, field string) credentialcrypto.EnvironmentSetupBinding {
	return credentialcrypto.EnvironmentSetupBinding{TenantID: uuid.UUID(tenant.Bytes).String(), Resource: resource, OwnerID: uuid.UUID(id.Bytes).String(), Field: field}
}

func fileBinding(tenant, id pgtype.UUID) credentialcrypto.EnvironmentFileBinding {
	return credentialcrypto.EnvironmentFileBinding{TenantID: uuid.UUID(tenant.Bytes).String(), Resource: resource, OwnerID: uuid.UUID(id.Bytes).String(), FileID: "files"}
}
