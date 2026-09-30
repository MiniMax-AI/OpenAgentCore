// Node-local adapter configuration and construction.
package providers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

type Config struct {
	Specification  sandbox.DeploymentSpec `json:"specification"`
	Generation     uint64                 `json:"generation"`
	CoreURL        string                 `json:"core_url"`
	Provider       string                 `json:"provider"`
	InstallationID string                 `json:"installation_id"`
	Docker         *Docker                `json:"docker,omitempty"`
	Microsandbox   *Microsandbox          `json:"microsandbox,omitempty"`
}

type Docker struct {
	Host          string   `json:"host"`
	Image         string   `json:"image"`
	Network       string   `json:"network"`
	SeccompFile   string   `json:"seccomp_file"`
	ExtraHosts    []string `json:"extra_hosts"`
	NestedSandbox bool     `json:"nested_sandbox"`
}

// Load rejects unknown fields, mixed adapters and explicit null configuration.
func Load(file string) (Config, error) {
	var config Config
	raw, err := os.ReadFile(file)
	if err != nil {
		return config, errors.New("cannot read sandbox configuration")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF {
		return config, errors.New("invalid sandbox configuration")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return config, errors.New("invalid sandbox configuration")
	}
	_, docker := fields["docker"]
	_, micro := fields["microsandbox"]
	if docker && micro {
		return config, errors.New("only one sandbox provider can be configured")
	}
	return config, nil
}

type Built struct {
	SpecificationDigest                string
	Provider                           sandbox.SandboxProvider
	InstallationID, BackendFingerprint string
	Probe                              func(context.Context) error
}

// LocalOptions supplies process-local context without changing persisted configuration.
type LocalOptions struct {
	// Standalone selects registration or execution without a generation manager.
	Standalone bool
	// GenerationStateDirectory is the node state directory when constructing a
	// retained generation. It must be canonical and absolute, and Standalone
	// must be false.
	GenerationStateDirectory string
}

func (r *Registry) Build(config Config, options LocalOptions) (*Built, func(), error) {
	closeProvider := func() {}
	if err := r.validateSpecification(config); err != nil {
		return nil, closeProvider, err
	}
	id, err := uuid.Parse(config.InstallationID)
	if err != nil || id == uuid.Nil || id.String() != config.InstallationID {
		return nil, closeProvider, errors.New("sandbox requires a canonical installation_id UUID")
	}
	adapter, err := r.Lookup(config.Provider)
	if err != nil || adapter.BuildLocal == nil {
		return nil, closeProvider, errors.New("sandbox provider is not node-local")
	}
	if options.Standalone {
		if options.GenerationStateDirectory != "" {
			return nil, closeProvider, sandbox.ErrInvalid
		}
	} else if !filepath.IsAbs(options.GenerationStateDirectory) || filepath.Clean(options.GenerationStateDirectory) != options.GenerationStateDirectory {
		return nil, closeProvider, sandbox.ErrInvalid
	}
	result := &Built{InstallationID: config.InstallationID, SpecificationDigest: config.Specification.Digest(config.Provider)}
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

func BackendFingerprint(kind, namespace string) string {
	digest := sha256.Sum256([]byte(kind + "\x00" + namespace))
	return hex.EncodeToString(digest[:])
}
