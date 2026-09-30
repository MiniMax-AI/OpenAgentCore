package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
)

// Requirement has no implicit default: registration must choose either value.
type Requirement string

const (
	Required    Requirement = "required"
	NotRequired Requirement = "not_required"
)

type ConfigurationRequirements struct {
	Credential   Requirement
	PublicOrigin Requirement
	Discovery    providercontract.Support
}

// Configuration is an adapter-owned typed value, never a request or response DTO.
// Implementations must exclude secrets from JSON and safe diagnostic output.
type Configuration interface {
	HasCredential() bool
	ReplacesCredential() bool
}

// ConfigurationRecord separates public selectors, read-only observations and
// secret bytes. Store encrypts Secret with the installation and generation.
// Only adapter codecs may produce Public and Metadata; neither is input passthrough.
type ConfigurationRecord struct {
	Public   json.RawMessage
	Metadata json.RawMessage
	Secret   []byte `json:"-"`
}

// ConfigurationAdapter owns all interpretation of provider configuration.
// Decode loads retained ownership without remote discovery or new-build admission.
// Normalize validates a candidate; ResolveChange first applies omitted-field
// inheritance, then normalizes. Equal compares normalized identity, excluding
// discovery metadata and explicit credential-submission intent.
type ConfigurationAdapter interface {
	Requirements() ConfigurationRequirements
	DecodeInput(public, credential json.RawMessage) (Configuration, error)
	Encode(Configuration) (ConfigurationRecord, error)
	Decode(ConfigurationRecord) (Configuration, error)
	Normalize(Selection) (Selection, error)
	ResolveChange(next, previous Selection) (Selection, error)
	WithCredential(owner, candidate Configuration) (Configuration, error)
	Equal(a, b Configuration) (bool, error)
}

// ConfigurationDiscoveryInput is a transient read-only request. Query is typed
// and validated by the adapter; it cannot select a compute mutation.
type ConfigurationDiscoveryInput struct {
	Configuration json.RawMessage `json:"configuration" swaggertype:"object"`
	Credential    json.RawMessage `json:"credential" swaggertype:"object"`
	Query         json.RawMessage `json:"query" swaggertype:"object"`
}

// ConfigurationDiscoverer is separate from compute and candidate admission.
// Support must also be explicitly declared in ConfigurationRequirements.
type ConfigurationDiscoverer interface {
	DiscoverConfiguration(context.Context, ConfigurationDiscoveryInput, ProcessPaths) (json.RawMessage, error)
}

// DecodeConfigurationObject rejects unknown fields, null, nonobjects and trailing
// input without exposing submitted content in its error. Missing objects are empty.
func DecodeConfigurationObject(raw json.RawMessage, target any, allowed ...string) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return ErrInvalid
	}
	for field, value := range fields {
		if !slices.Contains(allowed, field) || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrInvalid
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return ErrInvalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrInvalid
	}
	return nil
}
