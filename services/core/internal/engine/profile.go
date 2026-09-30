package engine

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

var ErrInvalidInput = errors.New("invalid engine configuration")

// Profile records qualified public behavior, independently of Runtime advertisements.
type Profile struct {
	ProgrammaticToolCallingDisable             proto.CapabilitySupport
	Placements                                 []string
	MCPOrigins                                 []string
	WebSearchControl, TextVerbosity, MCPBearer proto.CapabilitySupport
	StructuredOutput                           proto.CapabilitySupport
	ToolSearch                                 proto.CapabilitySupport
	MessageImages                              proto.CapabilitySupport
	ConfigurationValidation                    ValidationPolicy
	ToolsValidation                            ValidationPolicy
	FunctionResultValidation                   ValidationPolicy
	ValidateConfiguration                      func(agent v1.Agent, environment *v1.Environment, hasDaemon bool) error
	ValidateTools                              func(environment *v1.Environment, hasDaemon bool, functions []proto.FunctionTool, mcp []proto.MCPHTTPServer) error
	ValidateFunctionResult                     func(placement string, result proto.FunctionResultPayload) error
	// WhitespaceOnlyText qualifies messages without an image or non-whitespace
	// text. Unqualified harnesses reject them at admission; Core never trims or
	// pads model input to fit a harness.
	WhitespaceOnlyText proto.CapabilitySupport
}

func (p Profile) Accepts(placement string) bool {
	return slices.Contains(p.Placements, placement)
}

// Catalog is immutable after construction. Its zero value selects built-in profiles.
// NewCatalog with an empty map explicitly qualifies no engines.
type Catalog struct {
	profiles map[string]Profile
}

// NewCatalog rejects invalid static registrations with ErrInvalidDeclaration.
// Nil and empty maps both explicitly authorize no engines.
func NewCatalog(profiles map[string]Profile) Catalog {
	c := Catalog{profiles: make(map[string]Profile, len(profiles))}
	for kind, profile := range profiles {
		if kind == "" || kind != strings.TrimSpace(kind) {
			panic(ErrInvalidDeclaration)
		}
		if err := profile.ValidateDeclaration(); err != nil {
			panic(err)
		}
		profile.Placements = slices.Clone(profile.Placements)
		profile.MCPOrigins = slices.Clone(profile.MCPOrigins)
		c.profiles[kind] = profile
	}
	return c
}

func (c Catalog) Lookup(kind string) (Profile, bool) {
	if c.profiles == nil {
		c = qualified
	}
	profile, ok := c.profiles[kind]
	profile.Placements = slices.Clone(profile.Placements)
	profile.MCPOrigins = slices.Clone(profile.MCPOrigins)
	return profile, ok
}

// Kinds returns a stable snapshot of harnesses qualified by this build.
func (c Catalog) Kinds() []string {
	if c.profiles == nil {
		c = qualified
	}
	kinds := slices.Collect(maps.Keys(c.profiles))
	slices.Sort(kinds)
	return kinds
}

var ErrInvalidDeclaration = errors.New("invalid engine profile declaration")

// ValidationPolicy distinguishes an explicit reliance on common validation from
// additional pure Harness restrictions. Neither is an execution implementation.
type ValidationPolicy uint8

const (
	ValidationUnspecified ValidationPolicy = iota
	CommonValidationOnly
	AdditionalValidation
)

// ValidateDeclaration runs before a Catalog can authorize any operation. Every
// future field must be classified here; no field receives an inferred default.
func (p Profile) ValidateDeclaration() error {
	v := reflect.ValueOf(p)
	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i).Name
		if support, ok := v.Field(i).Interface().(proto.CapabilitySupport); ok {
			if support != proto.CapabilitySupported && support != proto.CapabilityUnsupported {
				return declarationError(field)
			}
			continue
		}
		switch field {
		case "Placements", "MCPOrigins":
			if v.Field(i).IsNil() {
				return declarationError(field)
			}
		case "ConfigurationValidation", "ToolsValidation", "FunctionResultValidation":
			policy, ok := v.Field(i).Interface().(ValidationPolicy)
			if !ok || (policy != CommonValidationOnly && policy != AdditionalValidation) {
				return declarationError(field)
			}
		case "ValidateConfiguration", "ValidateTools", "ValidateFunctionResult":
			// Paired with the explicit policy below, including legitimate nil.
		default:
			return declarationError(field)
		}
	}
	for _, strategy := range []struct {
		field  string
		policy ValidationPolicy
		custom bool
	}{
		{"ConfigurationValidation", p.ConfigurationValidation, p.ValidateConfiguration != nil},
		{"ToolsValidation", p.ToolsValidation, p.ValidateTools != nil},
		{"FunctionResultValidation", p.FunctionResultValidation, p.ValidateFunctionResult != nil},
	} {
		if (strategy.policy == AdditionalValidation) != strategy.custom {
			return declarationError(strategy.field)
		}
	}
	if len(p.Placements) == 0 || !validChoices(p.Placements, "none", "openai_hosted", "self_hosted") {
		return declarationError("Placements")
	}
	if !validChoices(p.MCPOrigins, "service", "environment") ||
		(slices.Contains(p.MCPOrigins, "service") && !p.Accepts("none")) ||
		(slices.Contains(p.MCPOrigins, "environment") && !p.Accepts("openai_hosted") && !p.Accepts("self_hosted")) ||
		(p.MCPBearer.IsSupported() && len(p.MCPOrigins) == 0) {
		return declarationError("MCPOrigins")
	}
	return nil
}

func declarationError(field string) error {
	// Only authored field names are exposed, never declaration values.
	return fmt.Errorf("%w: %s", ErrInvalidDeclaration, field)
}

func validChoices(values []string, allowed ...string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !slices.Contains(allowed, value) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}
