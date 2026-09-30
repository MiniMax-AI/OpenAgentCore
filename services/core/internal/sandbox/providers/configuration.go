package providers

import (
	"context"
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func (r *Registry) DecodeInput(kind string, public, credential json.RawMessage) (sandbox.Configuration, error) {
	a, err := r.Lookup(kind)
	if err != nil {
		return nil, err
	}
	return a.Configuration.DecodeInput(public, credential)
}
func (r *Registry) Encode(kind string, c sandbox.Configuration) (sandbox.ConfigurationRecord, error) {
	a, err := r.Lookup(kind)
	if err != nil {
		return sandbox.ConfigurationRecord{}, err
	}
	return a.Configuration.Encode(c)
}
func (r *Registry) Decode(kind string, record sandbox.ConfigurationRecord) (sandbox.Configuration, error) {
	a, err := r.Lookup(kind)
	if err != nil {
		return nil, err
	}
	return a.Configuration.Decode(record)
}
func (r *Registry) Equal(kind string, a, b sandbox.Configuration) (bool, error) {
	adapter, err := r.Lookup(kind)
	if err != nil {
		return false, err
	}
	return adapter.Configuration.Equal(a, b)
}
func (r *Registry) UsesCredential(kind string) (bool, error) {
	a, err := r.Lookup(kind)
	if err != nil {
		return false, err
	}
	if a.Configuration == nil {
		return false, providercontract.ErrContract
	}
	return required(a.Configuration.Requirements().Credential)
}
func (r *Registry) RequiresPublicOrigin(kind string) (bool, error) {
	a, err := r.Lookup(kind)
	if err != nil {
		return false, err
	}
	if a.Configuration == nil {
		return false, providercontract.ErrContract
	}
	return required(a.Configuration.Requirements().PublicOrigin)
}
func required(value sandbox.Requirement) (bool, error) {
	switch value {
	case sandbox.Required:
		return true, nil
	case sandbox.NotRequired:
		return false, nil
	default:
		return false, providercontract.ErrContract
	}
}
func (r *Registry) Normalize(s sandbox.Selection) (sandbox.Selection, error) {
	a, e := r.Lookup(s.Provider)
	if e != nil {
		return s, e
	}
	return a.Configuration.Normalize(s)
}
func (r *Registry) ResolveChange(next, previous sandbox.Selection) (sandbox.Selection, error) {
	a, e := r.Lookup(next.Provider)
	if e != nil {
		return next, e
	}
	return a.Configuration.ResolveChange(next, previous)
}
func (r *Registry) WithCredential(owner, candidate sandbox.Selection) (sandbox.Selection, error) {
	if owner.Provider != candidate.Provider {
		return owner, sandbox.ErrInvalid
	}
	a, e := r.Lookup(owner.Provider)
	if e != nil {
		return owner, e
	}
	needsCredential, e := r.UsesCredential(owner.Provider)
	if e != nil {
		return owner, e
	}
	if !needsCredential {
		return owner, &providercontract.UnsupportedError{Operation: "WithCredential", Reason: "credentials_not_required"}
	}
	owner.Configuration, e = a.Configuration.WithCredential(owner.Configuration, candidate.Configuration)
	return owner, e
}
func (r *Registry) DiscoverConfiguration(ctx context.Context, kind string, input sandbox.ConfigurationDiscoveryInput, paths sandbox.ProcessPaths) (json.RawMessage, error) {
	a, err := r.Lookup(kind)
	if err != nil {
		return nil, err
	}
	if err := a.Configuration.Requirements().Discovery.Check("DiscoverConfiguration"); err != nil {
		return nil, err
	}
	discovery, ok := a.Configuration.(sandbox.ConfigurationDiscoverer)
	if !ok {
		return nil, providercontract.ErrContract
	}
	return discovery.DiscoverConfiguration(ctx, input, paths)
}

type nodeConfiguration struct{}

func (nodeConfiguration) HasCredential() bool      { return false }
func (nodeConfiguration) ReplacesCredential() bool { return false }

type nodeConfigurationAdapter struct {
	validate func(sandbox.DeploymentSpec) error
}

func (nodeConfigurationAdapter) Requirements() sandbox.ConfigurationRequirements {
	return sandbox.ConfigurationRequirements{Credential: sandbox.NotRequired, PublicOrigin: sandbox.NotRequired, Discovery: providercontract.Support{State: providercontract.Unsupported, Reason: "node_configuration_has_no_catalog"}}
}
func (nodeConfigurationAdapter) DecodeInput(public, secret json.RawMessage) (sandbox.Configuration, error) {
	if len(secret) > 0 || sandbox.DecodeConfigurationObject(public, &struct{}{}) != nil {
		return nil, sandbox.ErrInvalid
	}
	return nodeConfiguration{}, nil
}
func (nodeConfigurationAdapter) Encode(c sandbox.Configuration) (sandbox.ConfigurationRecord, error) {
	if c != nil {
		if _, ok := c.(nodeConfiguration); !ok {
			return sandbox.ConfigurationRecord{}, sandbox.ErrInvalid
		}
	}
	return sandbox.ConfigurationRecord{Public: json.RawMessage(`{}`), Metadata: json.RawMessage(`{}`)}, nil
}
func (a nodeConfigurationAdapter) Decode(r sandbox.ConfigurationRecord) (sandbox.Configuration, error) {
	if len(r.Secret) > 0 || sandbox.DecodeConfigurationObject(r.Metadata, &struct{}{}) != nil {
		return nil, sandbox.ErrInvalid
	}
	return a.DecodeInput(r.Public, nil)
}
func (a nodeConfigurationAdapter) Normalize(s sandbox.Selection) (sandbox.Selection, error) {
	if _, err := a.Encode(s.Configuration); err != nil {
		return s, err
	}
	s.Configuration = nodeConfiguration{}
	return s, a.validate(s.DeploymentSpec)
}
func (a nodeConfigurationAdapter) ResolveChange(next, previous sandbox.Selection) (sandbox.Selection, error) {
	return a.Normalize(next)
}
func (nodeConfigurationAdapter) WithCredential(owner, candidate sandbox.Configuration) (sandbox.Configuration, error) {
	return nil, &providercontract.UnsupportedError{Operation: "WithCredential", Reason: "credentials_not_required"}
}
func (a nodeConfigurationAdapter) Equal(x, y sandbox.Configuration) (bool, error) {
	if _, err := a.Encode(x); err != nil {
		return false, err
	}
	if _, err := a.Encode(y); err != nil {
		return false, err
	}
	return true, nil
}
func (nodeConfigurationAdapter) DiscoverConfiguration(context.Context, sandbox.ConfigurationDiscoveryInput, sandbox.ProcessPaths) (json.RawMessage, error) {
	return nil, &providercontract.UnsupportedError{Operation: "DiscoverConfiguration", Reason: "node_configuration_has_no_catalog"}
}
