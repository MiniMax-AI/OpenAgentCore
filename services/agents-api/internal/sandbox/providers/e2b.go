package providers

import (
	"errors"
	"os"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/e2b"
)

type DirectConfig struct {
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
	return a.BuildDirect(c)
}
func buildE2B(c DirectConfig) (sandbox.SandboxProvider, error) {
	if c.Selection.E2B == nil {
		return nil, errors.New("E2B deployment configuration is unavailable")
	}
	binary := os.Getenv("OAC_E2B_PROVIDER_BIN")
	if binary == "" {
		binary = "/opt/oac/e2b/oac-e2b-provider"
	}
	// Only a candidate that omitted its resources has none; its validation
	// reads them from the template build before the candidate is rebuilt.
	var resources *sandbox.Resources
	if c.Selection.DeploymentSpec.Resources != (sandbox.Resources{}) {
		resources = &c.Selection.DeploymentSpec.Resources
	}
	provider, err := e2b.NewWithCaller(e2b.Config{Binary: binary, StateDir: os.Getenv("OAC_E2B_STATE_DIR"),
		Resources: resources, InstallationID: c.InstallationID, APIKey: c.Selection.E2B.APIKey, Template: c.Selection.E2B.Template,
		APIURL: c.Selection.E2B.APIURL, Domain: c.Selection.E2B.Domain, TimeoutSeconds: 3600}, &e2b.ProcessCaller{Fence: c.Fence})
	if err != nil {
		return nil, errors.New("E2B provider cannot load; check the installed helper and private state directory")
	}
	return provider, nil
}
