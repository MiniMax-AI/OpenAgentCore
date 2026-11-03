// Package opensandbox runs Core-managed sandboxes on an operator's OpenSandbox
// server through its official Go SDK. Sandboxes are found by their ownership
// metadata, so the adapter keeps no receipts or state of its own.
package opensandbox

import (
	"encoding/json"
	"net/url"
	"strings"
	"unicode"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func Policy() sandbox.DeploymentPolicy {
	return sandbox.DeploymentPolicy{
		DefaultResources: &sandbox.Resources{CPUs: 2, MemoryMiB: 4096},
		Network:          sandbox.NetworkSupport{Restrict: true},
	}
}

// Setup declares how the console connects an OpenSandbox server. The adapter
// reads its configuration and credential field names from here.
func Setup() sandbox.SetupDeclaration {
	return sandbox.SetupDeclaration{
		Label: "OpenSandbox",
		Summary: sandbox.Text{
			EN:   "Runs sandboxes on your own OpenSandbox server, on Docker or Kubernetes.",
			ZHCN: "沙箱运行在你自己的 OpenSandbox 服务上，支持 Docker 或 Kubernetes。",
		},
		Fields: []sandbox.SetupField{
			{Name: "api_url", Label: sandbox.Text{EN: "Server URL", ZHCN: "服务地址"}},
			{Name: "image", Label: sandbox.Text{EN: "Runtime image", ZHCN: "Runtime 镜像"},
				Help: sandbox.Text{EN: "Build it from the OpenAgentCore Runtime image with services/core/deploy/opensandbox/Dockerfile.", ZHCN: "用 services/core/deploy/opensandbox/Dockerfile 从 OpenAgentCore Runtime 镜像构建。"}},
			{Name: "api_key", Secret: true, Label: sandbox.Text{EN: "API key", ZHCN: "API 密钥"}},
		},
	}
}

// fieldNames lists the declared secret or non-secret field names.
func fieldNames(secret bool) []string {
	var names []string
	for _, f := range Setup().Fields {
		if f.Secret == secret {
			names = append(names, f.Name)
		}
	}
	return names
}

// DeploymentConfiguration keeps the API key out of JSON and diagnostics;
// ConfigurationAdapter.Encode owns the public projection. APIURL is the server
// origin: the adapter appends /v1.
type DeploymentConfiguration struct {
	CredentialSupplied bool   `json:"-"`
	APIKey             string `json:"-"`
	APIURL             string `json:"api_url"`
	Image              string `json:"image"`
}

func (c *DeploymentConfiguration) HasCredential() bool      { return c != nil && c.APIKey != "" }
func (c *DeploymentConfiguration) ReplacesCredential() bool { return c != nil && c.CredentialSupplied }
func (c *DeploymentConfiguration) String() string {
	return "OpenSandbox deployment configuration (private)"
}

// MarshalJSON blocks accidental serialization.
func (*DeploymentConfiguration) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }

type publicConfiguration struct {
	APIURL string `json:"api_url"`
	Image  string `json:"image"`
}

// normalizeURL returns the server origin without a trailing slash.
func normalizeURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 512 || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		strings.ContainsAny(raw, "?#") || (u.Path != "" && u.Path != "/") || strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return "", sandbox.ErrInvalid
	}
	return u.Scheme + "://" + u.Host, nil
}

func validImage(image string) bool {
	return image != "" && len(image) <= 512 && !strings.ContainsFunc(image, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

func validKey(key string) bool {
	return key != "" && len(key) <= 4096 && !strings.ContainsFunc(key, func(r rune) bool { return unicode.IsSpace(r) || r == 0 })
}

// validate normalizes the endpoint and checks every field of a complete configuration.
func validate(c *DeploymentConfiguration) (*DeploymentConfiguration, error) {
	if c == nil || !validKey(c.APIKey) || !validImage(c.Image) {
		return nil, sandbox.ErrInvalid
	}
	normalized := *c
	var err error
	if normalized.APIURL, err = normalizeURL(c.APIURL); err != nil {
		return nil, err
	}
	return &normalized, nil
}

func configuration(s sandbox.Selection) *DeploymentConfiguration {
	c, _ := s.Configuration.(*DeploymentConfiguration)
	return c
}

type ConfigurationAdapter struct{}

func (ConfigurationAdapter) Requirements() sandbox.ConfigurationRequirements {
	const reason = "opensandbox_has_no_catalog"
	return sandbox.ConfigurationRequirements{
		Credential: sandbox.Required, PublicOrigin: sandbox.Required,
		Discovery:              providercontract.Support{State: providercontract.Unsupported, Reason: reason},
		SelectionDiscovery:     providercontract.Support{State: providercontract.Unsupported, Reason: reason},
		CredentialVerification: providercontract.Support{State: providercontract.Supported},
	}
}

func (ConfigurationAdapter) DecodeInput(public, secret json.RawMessage) (sandbox.Configuration, error) {
	var p publicConfiguration
	if err := sandbox.DecodeConfigurationObject(public, &p, fieldNames(false)...); err != nil {
		return nil, err
	}
	c := &DeploymentConfiguration{APIURL: p.APIURL, Image: p.Image}
	if len(secret) > 0 {
		var credential struct {
			APIKey string `json:"api_key"`
		}
		if err := sandbox.DecodeConfigurationObject(secret, &credential, fieldNames(true)...); err != nil {
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
	public, err := json.Marshal(publicConfiguration{APIURL: c.APIURL, Image: c.Image})
	if err != nil {
		return sandbox.ConfigurationRecord{}, sandbox.ErrInvalid
	}
	return sandbox.ConfigurationRecord{Public: public, Secret: []byte(c.APIKey)}, nil
}

func (ConfigurationAdapter) Decode(record sandbox.ConfigurationRecord) (sandbox.Configuration, error) {
	var p publicConfiguration
	if len(record.Metadata) > 0 && string(record.Metadata) != "{}" || sandbox.DecodeConfigurationObject(record.Public, &p, fieldNames(false)...) != nil {
		return nil, sandbox.ErrInvalid
	}
	// A retained generation may hold no key; WithCredential composes the current one.
	apiURL, err := normalizeURL(p.APIURL)
	if err != nil || !validImage(p.Image) {
		return nil, sandbox.ErrInvalid
	}
	return &DeploymentConfiguration{APIURL: apiURL, Image: p.Image, APIKey: string(record.Secret)}, nil
}

func (ConfigurationAdapter) Normalize(s sandbox.Selection) (sandbox.Selection, error) {
	if s.Workspace != nil {
		return s, sandbox.ErrInvalid
	}
	if err := s.DeploymentSpec.ValidatePolicy("opensandbox", Policy()); err != nil {
		return s, err
	}
	c, err := validate(configuration(s))
	if err != nil {
		return s, err
	}
	s.Configuration = c
	return s, nil
}

// ResolveChange inherits the previous key when the change supplies none.
func (a ConfigurationAdapter) ResolveChange(next, previous sandbox.Selection) (sandbox.Selection, error) {
	if n, p := configuration(next), configuration(previous); n != nil && p != nil {
		c := *n
		c.CredentialSupplied = c.CredentialSupplied || c.APIKey != ""
		if c.APIKey == "" && !c.CredentialSupplied {
			c.APIKey = p.APIKey
		}
		next.Configuration = &c
	}
	return a.Normalize(next)
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
	xc.CredentialSupplied, yc.CredentialSupplied = false, false
	return xc == yc, nil
}
