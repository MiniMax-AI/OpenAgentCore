package providers

import (
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
)

type DirectConfig struct {
	ProcessPaths   sandbox.ProcessPaths
	InstallationID string
	Selection      sandbox.Selection
	Fence          *sandbox.CallFence
}

func BuildDirect(c DirectConfig) (sandbox.SandboxProvider, error) {
	a, e := Lookup(c.Selection.Provider)
	if e != nil {
		return nil, e
	}
	if a.BuildDirect == nil {
		return nil, sandbox.ErrInvalid
	}
	p, err := a.BuildDirect(c)
	if err != nil {
		return nil, err
	}
	if err := ValidateBinding(a, p); err != nil {
		return nil, err
	}
	return p, nil
}
func buildE2B(c DirectConfig) (sandbox.SandboxProvider, error) {
	configuration, ok := c.Selection.Configuration.(*e2b.DeploymentConfiguration)
	if !ok || configuration == nil {
		return nil, errors.New("E2B deployment configuration is unavailable")
	}
	binary, state, err := e2b.InstalledPaths(c.ProcessPaths)
	if err != nil {
		return nil, err
	}
	// Only a candidate that omitted its resources has none; its validation
	// reads them from the template build before the candidate is rebuilt.
	var resources *sandbox.Resources
	if c.Selection.DeploymentSpec.Resources != (sandbox.Resources{}) {
		resources = &c.Selection.DeploymentSpec.Resources
	}
	provider, err := e2b.NewWithCaller(e2b.Config{Binary: binary, StateDir: state,
		Resources: resources, InstallationID: c.InstallationID, APIKey: configuration.APIKey, Template: configuration.Template,
		APIURL: configuration.APIURL, Domain: configuration.Domain, TimeoutSeconds: 3600}, &e2b.ProcessCaller{Fence: c.Fence})
	if err != nil {
		return nil, errors.New("E2B provider cannot load; check the installed helper and private state directory")
	}
	return provider, nil
}
