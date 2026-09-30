package e2b

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func (ConfigurationAdapter) DiscoverConfiguration(ctx context.Context, input sandbox.ConfigurationDiscoveryInput, paths sandbox.ProcessPaths) (json.RawMessage, error) {
	var connection struct {
		APIURL string `json:"api_url"`
		Domain string `json:"domain"`
	}
	var credential struct {
		APIKey string `json:"api_key"`
	}
	var query struct {
		Template string `json:"template,omitempty"`
	}
	if sandbox.DecodeConfigurationObject(input.Configuration, &connection, "api_url", "domain") != nil || sandbox.DecodeConfigurationObject(input.Credential, &credential, "api_key") != nil || sandbox.DecodeConfigurationObject(input.Query, &query, "template") != nil {
		return nil, sandbox.ErrInvalid
	}
	binary, _, err := InstalledPaths(paths)
	if err != nil {
		return nil, sandbox.ErrConfigurationUnconfirmed
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := Discover(ctx, &ProcessCaller{}, binary, credential.APIKey, connection.APIURL, connection.Domain, query.Template)
	if err != nil {
		if errors.Is(err, sandbox.ErrInvalid) {
			return nil, sandbox.ErrConfigurationSelection
		}
		return nil, sandbox.ErrConfigurationUnconfirmed
	}
	if query.Template == "" {
		return json.Marshal(struct {
			Templates []TemplateSummary `json:"templates"`
		}{out.Templates})
	}
	return json.Marshal(struct {
		Builds []ReadyBuild `json:"builds"`
	}{out.Builds})
}
