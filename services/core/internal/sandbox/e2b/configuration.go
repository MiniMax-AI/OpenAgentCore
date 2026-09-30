package e2b

import (
	"encoding/json"
	"reflect"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

type ConfigurationAdapter struct{}
type publicConfiguration struct {
	Template string `json:"template"`
	APIURL   string `json:"api_url"`
	Domain   string `json:"domain"`
}
type buildMetadata struct {
	TemplateBuild buildView `json:"template_build"`
}
type buildView struct {
	Status    *string        `json:"status"`
	Resources buildResources `json:"resources"`
}
type buildResources struct {
	CPUs        *int32 `json:"cpus"`
	MemoryMiB   *int32 `json:"memory_mib"`
	RootDiskMiB *int32 `json:"root_disk_mib"`
}

func configuration(s sandbox.Selection) *DeploymentConfiguration {
	c, _ := s.Configuration.(*DeploymentConfiguration)
	return c
}
func (ConfigurationAdapter) Requirements() sandbox.ConfigurationRequirements {
	return sandbox.ConfigurationRequirements{Credential: sandbox.Required, PublicOrigin: sandbox.Required, Discovery: providercontract.Support{State: providercontract.Supported}}
}
func (ConfigurationAdapter) DecodeInput(public, secret json.RawMessage) (sandbox.Configuration, error) {
	var p publicConfiguration
	if err := sandbox.DecodeConfigurationObject(public, &p, "template", "api_url", "domain"); err != nil {
		return nil, err
	}
	c := &DeploymentConfiguration{Template: p.Template, APIURL: p.APIURL, Domain: p.Domain}
	if len(secret) > 0 {
		var credential struct {
			APIKey string `json:"api_key"`
		}
		if err := sandbox.DecodeConfigurationObject(secret, &credential, "api_key"); err != nil {
			return nil, err
		}
		if credential.APIKey == "" {
			return nil, sandbox.ErrInvalid
		}
		c.APIKey, c.CredentialSupplied = credential.APIKey, true
	}
	return c, nil
}
func (ConfigurationAdapter) Encode(value sandbox.Configuration) (sandbox.ConfigurationRecord, error) {
	c, ok := value.(*DeploymentConfiguration)
	if !ok || c == nil {
		return sandbox.ConfigurationRecord{}, sandbox.ErrInvalid
	}
	public, err := json.Marshal(publicConfiguration{Template: c.Template, APIURL: c.APIURL, Domain: c.Domain})
	if err != nil {
		return sandbox.ConfigurationRecord{}, sandbox.ErrInvalid
	}
	m := c.metadata
	if m != nil {
		copy := *m
		m = &copy
	}
	if b := c.TemplateBuild; b != nil {
		m = &buildMetadata{TemplateBuild: buildView{Status: &b.Status, Resources: buildResources{CPUs: &b.CPUs, MemoryMiB: &b.MemoryMiB, RootDiskMiB: b.RootDiskMiB}}}
	}
	var metadata json.RawMessage
	if m != nil {
		metadata, _ = json.Marshal(m)
	}
	return sandbox.ConfigurationRecord{Public: public, Metadata: metadata, Secret: []byte(c.APIKey)}, nil
}
func (ConfigurationAdapter) Decode(record sandbox.ConfigurationRecord) (sandbox.Configuration, error) {
	var p publicConfiguration
	var m buildMetadata
	if sandbox.DecodeConfigurationObject(record.Public, &p, "template", "api_url", "domain") != nil || sandbox.DecodeConfigurationObject(record.Metadata, &m, "template_build") != nil {
		return nil, sandbox.ErrInvalid
	}
	c := &DeploymentConfiguration{Template: p.Template, APIURL: p.APIURL, Domain: p.Domain, APIKey: string(record.Secret)}
	var err error
	c.APIURL, c.Domain, err = NormalizeEndpoint(c.APIURL, c.Domain)
	if err != nil {
		return nil, sandbox.ErrInvalid
	}
	if string(record.Metadata) != "{}" && len(record.Metadata) > 0 {
		c.metadata = &m
	}
	b := m.TemplateBuild
	if b.Status != nil && b.Resources.CPUs != nil && b.Resources.MemoryMiB != nil {
		c.TemplateBuild = &DeploymentBuild{Status: *b.Status, CPUs: *b.Resources.CPUs, MemoryMiB: *b.Resources.MemoryMiB, RootDiskMiB: b.Resources.RootDiskMiB}
	}
	return c, nil
}
func (ConfigurationAdapter) Normalize(s sandbox.Selection) (sandbox.Selection, error) {
	return NormalizeSelection(s)
}
func (a ConfigurationAdapter) ResolveChange(next, previous sandbox.Selection) (sandbox.Selection, error) {
	return a.Normalize(ResolveChange(next, previous))
}
func (ConfigurationAdapter) WithCredential(owner, candidate sandbox.Configuration) (sandbox.Configuration, error) {
	a, ok := owner.(*DeploymentConfiguration)
	b, ok2 := candidate.(*DeploymentConfiguration)
	if !ok || !ok2 || a == nil || b == nil || !b.HasCredential() {
		return nil, sandbox.ErrInvalid
	}
	c := *a
	c.APIKey = b.APIKey
	return &c, nil
}
func (ConfigurationAdapter) Equal(a, b sandbox.Configuration) (bool, error) {
	x, ok := a.(*DeploymentConfiguration)
	y, ok2 := b.(*DeploymentConfiguration)
	if !ok || !ok2 || x == nil || y == nil {
		return false, sandbox.ErrInvalid
	}
	xc, yc := *x, *y
	xc.TemplateBuild, yc.TemplateBuild = nil, nil
	xc.metadata, yc.metadata = nil, nil
	xc.CredentialSupplied, yc.CredentialSupplied = false, false
	return reflect.DeepEqual(xc, yc), nil
}
