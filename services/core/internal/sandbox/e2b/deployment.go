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
	return sandbox.DeploymentPolicy{RuntimeError: "E2B Runtime is selected by its immutable template build"}
}

func ValidateResources(r sandbox.Resources) error          { return r.ValidatePolicy("e2b", Policy()) }
func ValidateSpecification(s sandbox.DeploymentSpec) error { return s.ValidatePolicy("e2b", Policy()) }

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
// discovery. Persistence requires ValidateSpecification after preparation.
func NormalizeSelection(s sandbox.Selection) (sandbox.Selection, error) {
	if s.Resources == (sandbox.Resources{}) {
		if s.Runtime != nil {
			return s, &sandbox.ValidationError{Param: "runtime", Message: sandbox.ErrInvalid.Error() + ": " + Policy().RuntimeError}
		}
	} else if err := ValidateSpecification(s.DeploymentSpec); err != nil {
		return s, err
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
func (p *Provider) DiscoverSelection(ctx context.Context, s sandbox.Selection) (sandbox.Selection, error) {
	build, err := p.ValidateDeployment(ctx)
	if err != nil {
		if errors.Is(err, sandbox.ErrCredentialRejected) || errors.Is(err, sandbox.ErrCredentialOwnership) {
			return s, err
		}
		if errors.Is(err, sandbox.ErrInvalid) {
			return s, sandbox.ErrConfigurationSelection
		}
		return s, sandbox.ErrConfigurationUnconfirmed
	}
	recorded := &DeploymentBuild{Status: build.Status, CPUs: int32(build.CPUs), MemoryMiB: int32(build.MemoryMiB)}
	if build.RootDiskMiB != nil && *build.RootDiskMiB <= math.MaxInt32 {
		disk := int32(*build.RootDiskMiB)
		recorded.RootDiskMiB = &disk
	}
	s = WithTemplateBuild(s, recorded)
	if err := ValidateSpecification(s.DeploymentSpec); err != nil {
		return s, &sandbox.ValidationError{Param: "resources", Message: "E2B template build resources are outside the supported sandbox limits; select another build"}
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
