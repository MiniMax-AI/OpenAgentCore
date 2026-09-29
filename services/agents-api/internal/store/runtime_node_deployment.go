package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func configureRuntimeManager(ctx context.Context, q *sqlc.Queries, previous sqlc.RuntimeDeployment, selected *RuntimeDeployment) error {
	if selected.ProviderKind == "" {
		return nil
	}
	if selected.ProviderKind != "docker" && selected.ProviderKind != "microsandbox" {
		return ErrInvalidInput
	}
	installation, err := parseConnectionGeneration(selected.InstallationID)
	if err != nil {
		return err
	}
	if previous.ProviderKind == "" {
		resources, err := q.CountRuntimeDeploymentResources(ctx)
		if err != nil {
			return err
		}
		if resources.Allocations != 0 || resources.Pending != 0 {
			return fmt.Errorf("cannot adopt historical sandbox resources: keep the original Core responsible for retained resources and install this release separately")
		}
	}
	var localNode pgtype.UUID
	if selected.LocalNodeID != "" {
		id, err := parseConnectionGeneration(selected.LocalNodeID)
		if err != nil {
			return err
		}
		localNode = id
		if previous.LocalNodeID.Valid && previous.LocalNodeID != id {
			resources, err := q.CountRuntimeDeploymentResources(ctx)
			if err != nil {
				return err
			}
			if resources.Allocations != 0 || resources.Pending != 0 || !previous.AdmissionPaused || !selected.AdmissionPaused {
				return fmt.Errorf("local sandbox node identity changed: restore its original state directory; replacement requires maintenance and no retained resources")
			}
		}
		if !validRuntimeDigest(selected.LocalCredentialSHA256) {
			return ErrInvalidInput
		}
		if err := validateRuntimeNode("Local", selected.LocalMaxActive, selected.LocalMaxRetained); err != nil {
			return err
		}
		n, err := q.GetRuntimeNode(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = q.InsertRuntimeNode(ctx, sqlc.InsertRuntimeNodeParams{ID: id, InstallationID: installation, Name: "Local", BackendFingerprint: selected.BackendFingerprint, CredentialSha256: selected.LocalCredentialSHA256, MaxActive: int32(selected.LocalMaxActive), MaxRetained: int32(selected.LocalMaxRetained)})
		} else if err == nil {
			if n.InstallationID != installation || n.BackendFingerprint != selected.BackendFingerprint || n.CredentialSha256 != selected.LocalCredentialSHA256 {
				return fmt.Errorf("local sandbox node identity does not match the retained backend")
			}
			_, err = q.UpdateRuntimeNode(ctx, sqlc.UpdateRuntimeNodeParams{ID: id, Name: n.Name, MaxActive: int32(selected.LocalMaxActive), MaxRetained: int32(selected.LocalMaxRetained)})
		}
		if err != nil {
			return err
		}
	}
	return q.SetRuntimeManagerDeployment(ctx, sqlc.SetRuntimeManagerDeploymentParams{ProviderKind: selected.ProviderKind, LocalNodeID: localNode})
}
