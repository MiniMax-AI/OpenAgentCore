// Node-local adapter configuration and construction.
package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

// Load rejects unknown fields. The selected adapter decodes native at Build.
func Load(file string) (sandbox.NodeConfig, error) {
	var config sandbox.NodeConfig
	raw, err := os.ReadFile(file)
	if err != nil {
		return config, errors.New("cannot read sandbox configuration")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF {
		return config, errors.New("invalid sandbox configuration")
	}
	return config, nil
}

// Build validates the Core-owned part of a node configuration and passes it to
// the registered adapter. Local paths belong to the node; Core owns reservation
// capacity, execution resources and the immutable deployment release it
// enrolled with.
func (r *Registry) Build(config sandbox.NodeConfig, options sandbox.LocalOptions) (*sandbox.Built, func(), error) {
	closeProvider := func() {}
	adapter, err := r.Lookup(config.Provider)
	if err != nil {
		return nil, closeProvider, err
	}
	if adapter.Mode != sandbox.DeploymentNodes {
		return nil, closeProvider, fmt.Errorf("%w: selected provider does not support node hosting", sandbox.ErrInvalid)
	}
	if config.Generation == 0 {
		return nil, closeProvider, errors.New("node requires a deployment generation; obtain configuration from Core")
	}
	if config.Specification.Workspace != nil && options.Workspace == nil {
		return nil, closeProvider, sandbox.ErrInvalid
	}
	if err := adapter.ValidateSpecification(config.Specification); err != nil {
		return nil, closeProvider, err
	}
	id, err := uuid.Parse(config.InstallationID)
	if err != nil || id == uuid.Nil || id.String() != config.InstallationID {
		return nil, closeProvider, errors.New("sandbox requires a canonical installation_id UUID")
	}
	if options.Standalone {
		if options.GenerationStateDirectory != "" {
			return nil, closeProvider, sandbox.ErrInvalid
		}
	} else if !filepath.IsAbs(options.GenerationStateDirectory) || filepath.Clean(options.GenerationStateDirectory) != options.GenerationStateDirectory {
		return nil, closeProvider, sandbox.ErrInvalid
	}
	result := &sandbox.Built{InstallationID: config.InstallationID, SpecificationDigest: config.Specification.Digest(config.Provider)}
	closeProvider, err = adapter.BuildLocal(config, options, result)
	if err != nil {
		return nil, closeProvider, err
	}
	if err := ValidateBinding(adapter, result.Provider); err != nil {
		closeProvider()
		return nil, func() {}, err
	}
	return result, closeProvider, nil
}
