package e2b

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// DiscoverConfiguration lists templates for the declared template field, then
// the ready builds of the template chosen at the first level.
func (ConfigurationAdapter) DiscoverConfiguration(ctx context.Context, input sandbox.ConfigurationDiscoveryInput, paths sandbox.ProcessPaths) (sandbox.ConfigurationOptions, error) {
	var connection struct {
		APIURL string `json:"api_url"`
		Domain string `json:"domain"`
	}
	var credential struct {
		APIKey string `json:"api_key"`
	}
	path := input.Query.Path
	if sandbox.DecodeConfigurationObject(input.Configuration, &connection, fieldNames(false, false)...) != nil || sandbox.DecodeConfigurationObject(input.Credential, &credential, fieldNames(true, false)...) != nil || len(path) > 1 {
		return sandbox.ConfigurationOptions{}, sandbox.ErrInvalid
	}
	binary, _, err := InstalledPaths(paths)
	if err != nil {
		return sandbox.ConfigurationOptions{}, sandbox.ErrConfigurationUnconfirmed
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	template := ""
	if len(path) == 1 {
		template = path[0]
	}
	out, err := Discover(ctx, &ProcessCaller{}, binary, credential.APIKey, connection.APIURL, connection.Domain, template)
	if err != nil {
		if errors.Is(err, sandbox.ErrInvalid) {
			return sandbox.ConfigurationOptions{}, sandbox.ErrConfigurationSelection
		}
		return sandbox.ConfigurationOptions{}, sandbox.ErrConfigurationUnconfirmed
	}
	return discoveryOptions(out, template), nil
}

// discoveryOptions maps templates to incomplete options and the ready builds of
// the chosen template to complete ones.
func discoveryOptions(out Response, template string) sandbox.ConfigurationOptions {
	options := []sandbox.ConfigurationOption{}
	for _, t := range out.Templates {
		label := t.ID
		if len(t.Names) > 0 {
			label = t.Names[0] + " · " + t.ID
		}
		options = append(options, sandbox.ConfigurationOption{Value: t.ID, Label: label})
	}
	for _, b := range out.Builds {
		options = append(options, sandbox.ConfigurationOption{Value: template + ":" + b.ID, Label: b.ID, Resources: &sandbox.Resources{CPUs: b.CPUs, MemoryMiB: b.MemoryMiB}, Complete: true})
	}
	return sandbox.ConfigurationOptions{Options: options}
}
