package sandbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// GenerationStatus is one sparse observation, not a complete provider inventory.
// A serving pin is owned by Core and is independent of target preparation.
type GenerationStatus struct {
	Generation          uint64 `json:"generation"`
	SpecificationDigest string `json:"specification_digest"`
	State               string `json:"state"`
	Diagnostic          string `json:"diagnostic,omitempty"`
}

type GenerationReference struct {
	Generation          uint64 `json:"generation"`
	SpecificationDigest string `json:"specification_digest"`
}

type GenerationRetention struct {
	GenerationReference
	Keep bool `json:"keep"`
}

type NodeDeployment struct {
	SpecificationDigest string  `json:"specification_digest"`
	Generation          uint64  `json:"generation"`
	ServingGeneration   *uint64 `json:"serving_generation"`
}

// Omission or null cannot become an implicit destructive false grant.
func (g *GenerationRetention) UnmarshalJSON(raw []byte) error {
	var value struct {
		Generation          uint64 `json:"generation"`
		SpecificationDigest string `json:"specification_digest"`
		Keep                *bool  `json:"keep"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF || value.Keep == nil {
		return errors.New("invalid generation retention grant")
	}
	*g = GenerationRetention{GenerationReference: GenerationReference{Generation: value.Generation, SpecificationDigest: value.SpecificationDigest}, Keep: *value.Keep}
	return nil
}
