package e2b

import (
	"context"
	"errors"
	"math"
	"strings"
	"unicode"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
)

func Policy() sandbox.DeploymentPolicy {
	return sandbox.DeploymentPolicy{RuntimeError: "E2B Runtime is selected by its immutable template build"}
}

func ValidateResources(r sandbox.Resources) error          { return r.ValidatePolicy("e2b", Policy()) }
func ValidateSpecification(s sandbox.DeploymentSpec) error { return s.ValidatePolicy("e2b", Policy()) }

func ValidateConfiguration(c *sandbox.E2BConfiguration) error {
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
	if err := ValidateConfiguration(s.E2B); err != nil {
		return s, err
	}
	c := *s.E2B
	c.APIURL, c.Domain, _ = NormalizeEndpoint(c.APIURL, c.Domain)
	s.E2B = &c
	return s, nil
}

func WithTemplateBuild(input sandbox.Selection, build *sandbox.TemplateBuild) sandbox.Selection {
	if input.E2B != nil && build != nil {
		c, b := *input.E2B, *build
		if input.Resources == (sandbox.Resources{}) {
			input.Resources = sandbox.Resources{CPUs: uint32(b.CPUs), MemoryMiB: uint32(b.MemoryMiB)}
		}
		c.TemplateBuild = &b
		input.E2B = &c
	}
	return input
}

// DiscoverSelection checks the immutable native build without allocating compute.
func (p *Provider) DiscoverSelection(ctx context.Context, s sandbox.Selection) (sandbox.Selection, error) {
	build, err := p.ValidateDeployment(ctx)
	if err != nil {
		if errors.Is(err, ErrCredentialInvalid) || errors.Is(err, ErrTeamMismatch) {
			return s, err
		}
		if errors.Is(err, sandbox.ErrInvalid) {
			return s, ErrTemplateInvalid
		}
		return s, ErrRequestUnconfirmed
	}
	recorded := &sandbox.TemplateBuild{Status: build.Status, CPUs: int32(build.CPUs), MemoryMiB: int32(build.MemoryMiB)}
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

// RestoreSelection normalizes stored connection fields without admitting a new
// template or requiring remote availability for retained-resource cleanup.
func RestoreSelection(s sandbox.Selection) (sandbox.Selection, error) {
	if s.E2B == nil {
		return s, sandbox.ErrInvalid
	}
	c := *s.E2B
	var err error
	c.APIURL, c.Domain, err = NormalizeEndpoint(c.APIURL, c.Domain)
	s.E2B = &c
	return s, err
}
func ResolveChange(next, previous sandbox.Selection) sandbox.Selection {
	if next.E2B == nil || previous.E2B == nil {
		return next
	}
	c := *next.E2B
	c.ReplaceCredential = c.ReplaceCredential || c.APIKey != ""
	if c.APIURL == "" && c.Domain == "" {
		c.APIURL, c.Domain = previous.E2B.APIURL, previous.E2B.Domain
	}
	if c.APIKey == "" && !c.ReplaceCredential {
		c.APIKey = previous.E2B.APIKey
	}
	if c.Template == previous.E2B.Template && next.Resources == (sandbox.Resources{}) {
		next.Resources = previous.Resources
	}
	next.E2B = &c
	return next
}

func ReplaceCredential(owner, candidate sandbox.Selection) sandbox.Selection {
	if owner.E2B != nil && candidate.E2B != nil {
		c := *owner.E2B
		c.APIKey = candidate.E2B.APIKey
		owner.E2B = &c
	}
	return owner
}
func CredentialRequiresReset(err error) bool {
	return errors.Is(err, ErrCredentialInvalid) || errors.Is(err, ErrTeamMismatch)
}
