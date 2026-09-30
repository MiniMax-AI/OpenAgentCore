package store

import (
	"context"
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

func validateInitialInputs(inputs []Input) ([]Input, json.RawMessage, error) {
	for _, input := range inputs {
		if input.Kind != "message" {
			return nil, nil, ErrInvalidInput
		}
	}
	return validateInputs(inputs)
}

// The Session upsert locks retries. Only the new row reserves or admits work, so a
// retry after completion or later Turns cannot submit the original input again.
func (s *Store) createSessionResources(ctx context.Context, tenant string, params sqlc.CreateSessionParams, inputs []Input, encodedInput json.RawMessage, files []environmentconfig.InitialFile, setup environmentconfig.Setup, provider *v1.ModelProviderInput, executionConfiguration *v1.SessionExecutionConfiguration, providerSource string, deploymentRevision uuid.UUID) (sqlc.Session, *Environment, error) {
	var row sqlc.Session
	var environment *Environment
	err := s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		var err error
		row, err = q.CreateSession(ctx, params)
		if err != nil {
			return err
		}
		if row.ID == params.ID {
			var placement struct {
				Environment struct {
					Type string `json:"type"`
				} `json:"environment"`
			}
			if err := json.Unmarshal(row.Configuration, &placement); err != nil {
				return err
			}
			if placement.Environment.Type == "openai_hosted" {
				if err := checkRuntimeDeploymentAdmission(ctx, q, ""); err != nil {
					return err
				}
			}
			setup, err = s.freezeEnvironmentSkills(ctx, q, tenant, setup)
			if err != nil {
				return err
			}
			if err := s.saveSessionModelExecution(ctx, q, tenant, row.ID, provider); err != nil {
				return err
			}
			if err := saveSessionExecutionConfiguration(ctx, q, row, executionConfiguration, provider, providerSource, deploymentRevision); err != nil {
				return err
			}
			if len(files) > 0 {
				metadata, err := s.saveInitialFiles(ctx, q, tx, tenant, row.ID, files)
				if err != nil {
					return err
				}
				row, err = q.SetSessionInitialFileMetadata(ctx, sqlc.SetSessionInitialFileMetadataParams{ID: row.ID, Column2: metadata})
				if err != nil {
					return err
				}
			}
			if err := s.saveEnvironmentSetup(ctx, q, tenant, row.ID, setup); err != nil {
				return err
			}
			if !setup.Empty() {
				packages, err := json.Marshal(setup.PackageMetadata())
				if err != nil {
					return err
				}
				skills, err := json.Marshal(setup.SkillMetadata())
				if err != nil {
					return err
				}
				plugins, err := json.Marshal(setup.PluginMetadata())
				if err != nil {
					return err
				}
				directories, err := json.Marshal(append([]string{}, setup.CapabilityDirectories...))
				if err != nil {
					return err
				}
				row, err = q.SetSessionSetupMetadata(ctx, sqlc.SetSessionSetupMetadataParams{ID: row.ID, Packages: packages, Skills: skills, Plugins: plugins, CapabilityDirectories: directories})
				if err != nil {
					return err
				}
			}
			if err := createSessionEnvironment(ctx, q, row); err != nil {
				return err
			}
			if placement.Environment.Type == "openai_hosted" {
				if err := reserveRuntimePlacement(ctx, q, row.ID, s.publicURL); err != nil {
					return err
				}
			}
		}
		environment, err = sessionEnvironmentSnapshot(ctx, q, row)
		if err != nil {
			return err
		}
		audit := func() error {
			var created []writeaudit.Resource
			if row.ID == params.ID {
				created = append(created, writeaudit.Resource{Type: "session", ID: uuid.UUID(row.ID.Bytes).String()})
				if environment != nil {
					created = append(created, writeaudit.Resource{Type: "environment", ID: environment.ID, ParentID: uuid.UUID(row.ID.Bytes).String()})
				}
			}
			return auditpg.RecordWriteAudit(ctx, q, tenant, "create", "session", uuid.UUID(row.ID.Bytes).String(), "", created...)
		}
		if row.ID != params.ID || len(inputs) == 0 {
			return audit()
		}
		// Creation retries use the Session request hash. Keep the internal input key
		// independent of caller-supplied keys at the events endpoint.
		key := uuid.NewString()
		if environment != nil {
			// Environment input waits for the existing preparation and leased promotion.
			if err := withEnvironmentInputActivity(ctx, q, row.ID, func() error {
				_, err := q.CreateEnvironmentInputReservation(ctx, sqlc.CreateEnvironmentInputReservationParams{
					ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, SessionID: row.ID,
					IdempotencyKey: key, Batch: encodedInput, IsInitial: true,
				})
				return err
			}); err != nil {
				return err
			}
		} else {
			for position, input := range inputs {
				if _, err := admitInput(ctx, q, tenant, row.ID, key, int32(position), input); err != nil {
					return err
				}
			}
		}
		if err := sessionpg.PruneChanges(ctx, q, row.ID); err != nil {
			return err
		}
		return audit()
	})
	return row, environment, err
}
