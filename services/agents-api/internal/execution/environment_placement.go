package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentnetwork"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type environmentPlacement struct {
	Skills                []store.EnvironmentSkillMetadata `json:"skills,omitempty"`
	Type                  string                           `json:"type"`
	ToolEnvironment       bool                             `json:"initialization,omitempty"`
	SystemPackages        bool                             `json:"-"`
	NetworkAccess         string                           `json:"-"`
	AllowedDomains        []string                         `json:"-"`
	WorkspaceDirectory    string                           `json:"workspace_directory"`
	CapabilityDirectories []string                         `json:"capability_directories"`
}

// LocalWorkspaceConfiguration recognizes the qualified stored V1 profile. It
// does not provision a Runtime, validate live authority, or admit hosted creation.
func LocalWorkspaceConfiguration(configuration json.RawMessage) bool {
	placement, err := parseEnvironmentPlacement(configuration)
	return err == nil && placement.Type == "openai_hosted"
}

func parseEnvironmentPlacement(configuration json.RawMessage) (environmentPlacement, error) {
	var placement environmentPlacement
	if json.Unmarshal(configuration, &placement) != nil {
		return placement, store.ErrInvalidInput
	}
	switch placement.Type {
	case "self_hosted":
		if placement.WorkspaceDirectory != "" && len(placement.CapabilityDirectories) == 0 {
			return placement, nil
		}
	case "openai_hosted":
		// Stored policy is shared by preparation and provider bootstrap.
		var local struct {
			Skills                []store.EnvironmentSkillMetadata `json:"skills,omitempty"`
			Files                 []store.InitialFileMetadata      `json:"files"`
			Packages              *v1.EnvironmentPackages          `json:"packages,omitempty"`
			Initialization        bool                             `json:"initialization,omitempty"`
			Type                  string                           `json:"type"`
			CapabilityDirectories []string                         `json:"capability_directories"`
			Network               *struct {
				Access         string   `json:"access"`
				AllowedDomains []string `json:"allowed_domains"`
			} `json:"network"`
		}
		decoder := json.NewDecoder(bytes.NewReader(configuration))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&local) == nil && len(local.CapabilityDirectories) == 0 {
			placement.SystemPackages = local.Packages != nil && len(local.Packages.System) > 0
			placement.NetworkAccess = "enabled"
			if local.Network != nil {
				if (agentnetwork.Policy{Access: local.Network.Access, AllowedDomains: local.Network.AllowedDomains}).Validate() != nil {
					return placement, store.ErrInvalidInput
				}
				placement.NetworkAccess = local.Network.Access
				placement.AllowedDomains = append([]string(nil), local.Network.AllowedDomains...)
			}
			return placement, nil
		}
	}
	return placement, store.ErrInvalidInput
}

func environmentDeviceMatches(session store.Session, environment store.Environment, bound store.ExecutionDevice, placement environmentPlacement) bool {
	if environment.SessionID != session.ID || environment.TenantID != session.TenantID {
		return false
	}
	if placement.Type == "openai_hosted" {
		return bound.EnvironmentID == environment.ID
	}
	return bound.EnvironmentID == ""
}

func (d *Dispatcher) configurePreparedEnvironment(ctx context.Context, session store.Session, environment store.Environment, bound store.ExecutionDevice, req *proto.PromptRequestPayload) (func(), error) {
	placement, err := parseEnvironmentPlacement(environment.Configuration)
	if err != nil || !environmentDeviceMatches(session, environment, bound, placement) {
		return nil, store.ErrInvalidInput
	}
	if placement.Type == "openai_hosted" {
		if placement.SystemPackages && !placement.ToolEnvironment {
			return nil, store.ErrInvalidInput
		}
		req.LocalEnvironment = &proto.LocalEnvironment{ID: environment.ID, ToolEnvironment: placement.ToolEnvironment, SystemPackages: placement.SystemPackages}
		for _, metadata := range placement.Skills {
			if store.ValidateInstalledSkillMetadata(metadata) != nil {
				return nil, store.ErrInvalidInput
			}
			req.LocalEnvironment.Skills = append(req.LocalEnvironment.Skills, (store.EnvironmentSkill{Metadata: metadata}).InstallationMetadata())
		}
		req.LocalEnvironment.NetworkAccess = placement.NetworkAccess
		req.LocalEnvironment.AllowedDomains = append([]string(nil), placement.AllowedDomains...)
		return nil, nil
	}
	if d.EnvironmentConnection == nil {
		return nil, errors.New("environment connection resolver is not configured")
	}
	connection, err := d.EnvironmentConnection(ctx, session, environment)
	if err != nil {
		return connection.Release, err
	}
	if connection.URL == "" || connection.Token == "" || connection.Release == nil {
		return connection.Release, errors.New("environment connection is incomplete")
	}
	req.RemoteEnvironment = &proto.RemoteEnvironment{ID: environment.ID, WorkspaceDirectory: placement.WorkspaceDirectory, ConnectionURL: connection.URL, ConnectionToken: connection.Token}
	return connection.Release, nil
}
