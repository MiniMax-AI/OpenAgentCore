package proto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
