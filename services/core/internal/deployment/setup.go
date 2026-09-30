package deployment

import (
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// Setup is the immutable configuration selected by the deployment admin, or one
// generation of it. An empty Provider means that Web setup has not yet selected
// an adapter.
type Setup struct {
	Specification                                sandbox.DeploymentSpec
	InstallationID, Provider, BackendFingerprint string
	Generation                                   uint64
	// Mode is where the provider runs: "nodes" for enrolled sandbox nodes and
	// "direct" for a provider Core calls itself.
	Mode            string
	AdmissionPaused bool
	// Operations is the provider's declared operation support, which the node
	// transport proxies.
	Operations    providercontract.Operations
	Configuration sandbox.Configuration `json:"-"`
	// Suspension is the idle suspension policy of a provider that suspends
	// sandboxes, and nil otherwise.
	Suspension *Suspension
	// UsesCredential reports whether the provider's configuration carries a
	// credential.
	UsesCredential bool
}

// selection is the provider selection a Setup describes.
func (s Setup) selection() sandbox.Selection {
	return sandbox.Selection{Provider: s.Provider, DeploymentSpec: s.Specification, Configuration: s.Configuration}
}

// ProcessDeployment is a deployment selected by process configuration instead of
// Web setup. Its fingerprint describes the backend namespace, never credentials
// or image contents.
type ProcessDeployment struct {
	ProviderKind                     string
	LocalNodeID                      string
	LocalCredentialSHA256            string
	LocalMaxActive, LocalMaxRetained int
	InstallationID                   string
	BackendFingerprint               string
	AdmissionPaused                  bool
}

// configurationJSON reports an absent configuration object as {}.
func configurationJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}
