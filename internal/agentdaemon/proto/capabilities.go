package proto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
)

// CapabilitySupport requires a deliberate decision. Its zero value is invalid;
// adding a field cannot silently advertise an implementation as unsupported.
type CapabilitySupport uint8

const (
	CapabilityUnspecified CapabilitySupport = iota
	CapabilityUnsupported
	CapabilitySupported
)

func (s CapabilitySupport) IsSupported() bool { return s == CapabilitySupported }

// CapabilityFromBool converts one explicitly selected capability decision.
func CapabilityFromBool(supported bool) CapabilitySupport {
	if supported {
		return CapabilitySupported
	}
	return CapabilityUnsupported
}

func (s CapabilitySupport) MarshalJSON() ([]byte, error) {
	switch s {
	case CapabilitySupported:
		return []byte("true"), nil
	case CapabilityUnsupported:
		return []byte("false"), nil
	default:
		return nil, errors.New("capability support must be explicitly declared")
	}
}

func (s *CapabilitySupport) UnmarshalJSON(data []byte) error {
	switch string(bytes.TrimSpace(data)) {
	case "true":
		*s = CapabilitySupported
	case "false":
		*s = CapabilityUnsupported
	default:
		return errors.New("capability support must be a boolean")
	}
	return nil
}

// ValidateDeclaration covers every field, including future additions. The
// struct remains flat so new fields cannot bypass validation through nesting.
func (c AgentKindCapabilities) ValidateDeclaration() error {
	v := reflect.ValueOf(c)
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		s, ok := v.Field(i).Interface().(CapabilitySupport)
		if !ok || (s != CapabilitySupported && s != CapabilityUnsupported) {
			return fmt.Errorf("capability %s must be explicitly declared", t.Field(i).Name)
		}
	}
	return nil
}

func (c AgentKindCapabilities) MarshalJSON() ([]byte, error) {
	if err := c.ValidateDeclaration(); err != nil {
		return nil, err
	}
	type declaration AgentKindCapabilities
	return json.Marshal(declaration(c))
}

func (c *AgentKindCapabilities) UnmarshalJSON(data []byte) error {
	type declaration AgentKindCapabilities
	var decoded declaration
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		// Do not echo unknown keys or untrusted payload values.
		return errors.New("invalid capability declaration")
	}
	result := AgentKindCapabilities(decoded)
	if err := result.ValidateDeclaration(); err != nil {
		return err
	}
	*c = result
	return nil
}

// Declaration is one Harness's support, authored once in
// internal/harnessconfig/<kind>. Capabilities is the maximum a Runtime may
// advertise; discovery and the Environment owner only narrow it. The other
// fields never change at runtime and are never sent: Core reads them from the
// built-in declaration. ValidateSelection is the only reader.
type Declaration struct {
	Capabilities AgentKindCapabilities
	// WhitespaceOnlyText admits a message without an image or any
	// non-whitespace text.
	WhitespaceOnlyText CapabilitySupport
	// FunctionResultImageURLs admits remote image references in function
	// results; otherwise their images are inline PNG or JPEG.
	FunctionResultImageURLs CapabilitySupport
	// FailedFunctionResultImages admits images in a failed function result.
	FailedFunctionResultImages CapabilitySupport
	// MCPAllowedTools admits an MCP allowed_tools list; otherwise it is null.
	MCPAllowedTools CapabilitySupport
	// MCPOrigins lists the MCP connection origins the Harness serves.
	MCPOrigins []string
	// ReservedMCPLabels are MCP server labels the adapter uses itself.
	ReservedMCPLabels []string
	// MCPLabel and MCPToolName, when set, restrict MCP server labels and
	// allowed tool names.
	MCPLabel, MCPToolName *regexp.Regexp
	// Binary64OutputSchema requires a json_schema output schema with an
	// object root and numbers that survive binary64.
	Binary64OutputSchema bool
	// Conflicts lists pairs of features the Harness supports alone but not
	// together.
	Conflicts [][2]Feature
}

// ValidateDeclaration rejects an incomplete or ambiguous static declaration.
func (d Declaration) ValidateDeclaration() error {
	if err := d.Capabilities.ValidateDeclaration(); err != nil {
		return err
	}
	for name, s := range map[string]CapabilitySupport{"WhitespaceOnlyText": d.WhitespaceOnlyText, "FunctionResultImageURLs": d.FunctionResultImageURLs,
		"FailedFunctionResultImages": d.FailedFunctionResultImages, "MCPAllowedTools": d.MCPAllowedTools} {
		if s != CapabilitySupported && s != CapabilityUnsupported {
			return fmt.Errorf("declaration %s must be explicitly declared", name)
		}
	}
	for i, origin := range d.MCPOrigins {
		if (origin != "service" && origin != "environment") || slices.Contains(d.MCPOrigins[:i], origin) {
			return errors.New("declaration MCPOrigins must list distinct service and environment origins")
		}
	}
	for _, pair := range d.Conflicts {
		if !pair[0].valid() || !pair[1].valid() || pair[0] == pair[1] {
			return errors.New("declaration Conflicts must pair two distinct features")
		}
	}
	return nil
}

// Narrow returns d with the capabilities a Runtime advertises, which may
// only clear support that d declares.
func (d Declaration) Narrow(advertised AgentKindCapabilities) (Declaration, error) {
	if err := advertised.ValidateDeclaration(); err != nil {
		return Declaration{}, err
	}
	declared, narrowed := reflect.ValueOf(d.Capabilities), reflect.ValueOf(advertised)
	for i := 0; i < declared.NumField(); i++ {
		if narrowed.Field(i).Interface() == CapabilitySupported && declared.Field(i).Interface() != CapabilitySupported {
			return Declaration{}, fmt.Errorf("capability %s widens the Harness declaration", declared.Type().Field(i).Name)
		}
	}
	d.Capabilities = advertised
	return d, nil
}

func (k SupportedAgentKind) ValidateDeclaration() error {
	if k.Kind == "" {
		return errors.New("agent kind is required")
	}
	return k.Capabilities.ValidateDeclaration()
}

func (k *SupportedAgentKind) UnmarshalJSON(data []byte) error {
	type descriptor SupportedAgentKind
	var decoded descriptor
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	result := SupportedAgentKind(decoded)
	if err := result.ValidateDeclaration(); err != nil {
		return err
	}
	*k = result
	return nil
}
