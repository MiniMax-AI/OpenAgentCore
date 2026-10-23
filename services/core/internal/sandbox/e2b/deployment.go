package e2b

import (
	"context"
	"errors"
	"math"
	"strings"
	"unicode"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func Policy() sandbox.DeploymentPolicy {
	return sandbox.DeploymentPolicy{}
}

// Setup declares how the console connects E2B and compatible services. The
// adapter reads its configuration and credential field names from here.
func Setup() sandbox.SetupDeclaration {
	return sandbox.SetupDeclaration{
		Label: "E2B",
		Summary: sandbox.Text{
			EN:   "Runs sandboxes in a hosted E2B-compatible cloud: no machines to manage, billed by the service.",
			ZHCN: "沙箱运行在托管的 E2B 兼容云端：无需管理机器，由服务方计费。",
		},
		Fields: []sandbox.SetupField{
			{Name: "api_key", Secret: true, Label: sandbox.Text{EN: "API key", ZHCN: "API 密钥"}},
			{Name: "template", Discovered: true, Label: sandbox.Text{EN: "Template build", ZHCN: "模板构建"},
				Help: sandbox.Text{EN: "Core validates the exact ready build again when you save.", ZHCN: "保存时 Core 会再次校验这个已就绪的构建。"}},
			{Name: "api_url", Optional: true, Label: sandbox.Text{EN: "Sandbox API URL", ZHCN: "沙箱 API 地址"},
				Help: sandbox.Text{EN: "The selected service supplies a default. You can edit it for a compatible endpoint.", ZHCN: "所选服务会提供默认值，兼容的端点可以自行修改。"}},
			{Name: "domain", Optional: true, Label: sandbox.Text{EN: "Sandbox data-plane domain", ZHCN: "沙箱数据面域名"}},
		},
		Presets: []sandbox.SetupPreset{
			{Label: "SandBase Sandbox", Values: map[string]string{"api_url": "https://sandbox.sandbase.ai", "domain": "sandbox.sandbase.ai"}, CredentialURL: "https://www.sandbase.ai/console/keys"},
			{Label: "E2B", Values: map[string]string{"api_url": "https://api.e2b.app", "domain": "e2b.app"}, CredentialURL: "https://e2b.dev/dashboard?tab=api-keys"},
		},
	}
}

// fieldNames lists the declared secret or non-secret field names, discovered ones only when asked.
func fieldNames(secret, discovered bool) []string {
	var names []string
	for _, f := range Setup().Fields {
		if f.Secret == secret && (discovered || !f.Discovered) {
			names = append(names, f.Name)
		}
	}
	return names
}

func ValidateConfiguration(c *DeploymentConfiguration) error {
	if c == nil || c.APIKey == "" || len(c.APIKey) > 4096 || strings.IndexFunc(c.APIKey, func(r rune) bool { return unicode.IsSpace(r) || r == 0 }) >= 0 {
		return sandbox.ErrInvalid
	}
	if _, _, err := NormalizeEndpoint(c.APIURL, c.Domain); err != nil {
		return sandbox.ErrInvalid
	}
	template, build, ok := strings.Cut(c.Template, ":")
	id, err := uuid.Parse(build)
	if !ok || template == "" || len(template) > 128 || err != nil || id == uuid.Nil || id.String() != build {
		return sandbox.ErrInvalid
	}
	for _, c := range template {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return sandbox.ErrInvalid
		}
	}
	return nil
}

// NormalizeSelection accepts omitted candidate resources only until live build
// discovery. Persistence requires a valid specification after preparation.
func NormalizeSelection(s sandbox.Selection) (sandbox.Selection, error) {
	if s.Workspace != nil {
		return s, sandbox.ErrInvalid
	}
	if s.Resources != (sandbox.Resources{}) {
		if err := s.DeploymentSpec.ValidatePolicy("e2b", Policy()); err != nil {
			return s, err
		}
	}
	if err := ValidateConfiguration(configuration(s)); err != nil {
		return s, err
	}
	c := *configuration(s)
	c.APIURL, c.Domain, _ = NormalizeEndpoint(c.APIURL, c.Domain)
	s.Configuration = &c
	return s, nil
}

func WithTemplateBuild(input sandbox.Selection, build *DeploymentBuild) sandbox.Selection {
	if configuration(input) != nil && build != nil {
		c, b := *configuration(input), *build
		if input.Resources == (sandbox.Resources{}) {
			input.Resources = sandbox.Resources{CPUs: uint32(b.CPUs), MemoryMiB: uint32(b.MemoryMiB)}
		}
		c.TemplateBuild = &b
		input.Configuration = &c
	}
	return input
}

// DiscoverSelection checks the immutable native build without allocating compute.
func (ConfigurationAdapter) DiscoverSelection(ctx context.Context, c sandbox.DirectConfig) (sandbox.Selection, error) {
	p, err := newDirect(c)
	if err != nil {
		return sandbox.Selection{}, err
	}
	build, err := p.ValidateDeployment(ctx)
	if err != nil {
		if errors.Is(err, sandbox.ErrCredentialRejected) || errors.Is(err, sandbox.ErrCredentialOwnership) {
			return sandbox.Selection{}, err
		}
		if errors.Is(err, sandbox.ErrInvalid) {
			return sandbox.Selection{}, sandbox.ErrConfigurationSelection
		}
		return sandbox.Selection{}, sandbox.ErrConfigurationUnconfirmed
	}
	recorded := &DeploymentBuild{Status: build.Status, CPUs: int32(build.CPUs), MemoryMiB: int32(build.MemoryMiB)}
	if build.RootDiskMiB != nil && *build.RootDiskMiB <= math.MaxInt32 {
		disk := int32(*build.RootDiskMiB)
		recorded.RootDiskMiB = &disk
	}
	s := WithTemplateBuild(c.Selection, recorded)
	if err := s.DeploymentSpec.ValidatePolicy("e2b", Policy()); err != nil {
		return sandbox.Selection{}, &sandbox.ValidationError{Param: "resources", Message: "E2B template build resources are outside the supported sandbox limits; select another build"}
	}
	return s, nil
}

func ResolveChange(next, previous sandbox.Selection) sandbox.Selection {
	if configuration(next) == nil || configuration(previous) == nil {
		return next
	}
	c := *configuration(next)
	c.CredentialSupplied = c.CredentialSupplied || c.APIKey != ""
	if c.APIURL == "" && c.Domain == "" {
		c.APIURL, c.Domain = configuration(previous).APIURL, configuration(previous).Domain
	}
	if c.APIKey == "" && !c.CredentialSupplied {
		c.APIKey = configuration(previous).APIKey
	}
	if c.Template == configuration(previous).Template && next.Resources == (sandbox.Resources{}) {
		next.Resources = previous.Resources
	}
	next.Configuration = &c
	return next
}
