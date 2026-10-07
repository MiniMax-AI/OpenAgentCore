package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
	result, err := a.Configuration.DiscoverConfiguration(ctx, input, paths)
	if err != nil {
		return nil, declaredResult("DiscoverConfiguration", err)
	}
	return result, nil
}

// DiscoverSelection resolves a direct candidate's omitted native values.
func (r *Registry) DiscoverSelection(ctx context.Context, c sandbox.DirectConfig) (sandbox.Selection, error) {
	a, err := r.Lookup(c.Selection.Provider)
	if err != nil {
		return sandbox.Selection{}, err
	}
	if err := a.Configuration.Requirements().SelectionDiscovery.Check("DiscoverSelection"); err != nil {
		return sandbox.Selection{}, err
	}
	s, err := a.Configuration.DiscoverSelection(ctx, c)
	if err != nil {
		return sandbox.Selection{}, declaredResult("DiscoverSelection", err)
	}
	return s, nil
}

// VerifyCredential checks a candidate credential against owned resources.
func (r *Registry) VerifyCredential(ctx context.Context, c sandbox.DirectConfig, refs []sandbox.Reference) error {
	a, err := r.Lookup(c.Selection.Provider)
	if err != nil {
		return err
	}
	if err := a.Configuration.Requirements().CredentialVerification.Check("VerifyCredential"); err != nil {
		return err
	}
	return declaredResult("VerifyCredential", a.Configuration.VerifyCredential(ctx, c, refs))
}

// declaredResult keeps a supported declaration binding: an operation declared
// Supported that reports Unsupported breaks the contract.
func declaredResult(operation string, err error) error {
	if errors.Is(err, providercontract.ErrUnsupported) {
		return fmt.Errorf("%w: %s is declared supported", providercontract.ErrContract, operation)
	}
	return err
}

type nodeConfiguration struct{}

func (nodeConfiguration) HasCredential() bool      { return false }
func (nodeConfiguration) ReplacesCredential() bool { return false }

type nodeConfigurationAdapter struct {
	validate func(sandbox.DeploymentSpec) error
}

func (nodeConfigurationAdapter) Requirements() sandbox.ConfigurationRequirements {
	return sandbox.ConfigurationRequirements{Credential: sandbox.NotRequired,
		Discovery:              providercontract.Support{State: providercontract.Unsupported, Reason: "node_configuration_has_no_catalog"},
		SelectionDiscovery:     providercontract.Support{State: providercontract.Unsupported, Reason: "node_configuration_has_no_catalog"},
		CredentialVerification: providercontract.Support{State: providercontract.Unsupported, Reason: "credentials_not_required"}}
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
func (nodeConfigurationAdapter) DiscoverSelection(context.Context, sandbox.DirectConfig) (sandbox.Selection, error) {
	return sandbox.Selection{}, &providercontract.UnsupportedError{Operation: "DiscoverSelection", Reason: "node_configuration_has_no_catalog"}
}
func (nodeConfigurationAdapter) VerifyCredential(context.Context, sandbox.DirectConfig, []sandbox.Reference) error {
	return &providercontract.UnsupportedError{Operation: "VerifyCredential", Reason: "credentials_not_required"}
}
