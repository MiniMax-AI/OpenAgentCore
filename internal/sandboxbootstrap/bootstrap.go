// Package sandboxbootstrap owns the Provider-to-Sandbox I/O service startup
// input. Providers deliver this document as a private file; only the service
// interprets it. docs/sandbox-bootstrap.md describes it.
package sandboxbootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/google/uuid"
)

const Version = 1
const MaxBytes = 16 * 1024

var ErrInvalid = errors.New("invalid Sandbox I/O bootstrap input")

// Input is a current-version launch input. The service runs as the account
// the Provider starts it with; the input selects no other identity.
type Input struct {
	Version    int      `json:"version"`
	LinkURL    string   `json:"link_url"`
	Credential string   `json:"credential"`
	Resource   Resource `json:"resource"`
}

// Resource is the sandbox the serve credential serves: a Link ResourceRef.
type Resource struct {
	TenantID      string `json:"tenant_id"`
	EnvironmentID string `json:"environment_id"`
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	Generation    uint64 `json:"generation"`
}

var resourceKinds = map[string]sandboxlink.ResourceKind{
	"allocation": sandboxlink.ResourceAllocation,
	"enrollment": sandboxlink.ResourceEnrollment,
}

func (in Input) Validate() error {
	if in.Version != Version || sandboxlink.CheckRelayURL(in.LinkURL) != nil ||
		in.Credential == "" || len(in.Credential) > sandboxlink.MaxCredentialBytes ||
		strings.ContainsFunc(in.Credential, func(r rune) bool { return r == 0 || unicode.IsSpace(r) }) ||
		in.Resource.validate() != nil {
		return ErrInvalid
	}
	raw, err := json.Marshal(in)
	if err != nil || len(raw) > MaxBytes {
		return ErrInvalid
	}
	return nil
}

func (r Resource) validate() error {
	for _, id := range []string{r.TenantID, r.EnvironmentID, r.ID} {
		if u, err := uuid.Parse(id); err != nil || u == uuid.Nil || u.String() != id {
			return ErrInvalid
		}
	}
	if _, ok := resourceKinds[r.Kind]; !ok || r.Generation == 0 {
		return ErrInvalid
	}
	return nil
}

// Ref returns the resource as the Link names it. r must be valid.
func (r Resource) Ref() sandboxlink.ResourceRef {
	id := func(s string) sandboxwire.ID { return sandboxwire.ID(uuid.MustParse(s)) }
	return sandboxlink.ResourceRef{TenantID: id(r.TenantID), EnvironmentID: id(r.EnvironmentID),
		Kind: resourceKinds[r.Kind], ID: id(r.ID), Generation: r.Generation}
}

func (in Input) Marshal() ([]byte, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(in)
}

// Decode accepts one exact object. Unknown, duplicate, missing and case-aliased
// fields reject, at every level, instead of introducing alternate spellings of
// this contract.
func Decode(raw []byte) (Input, error) {
	var in Input
	if len(raw) > MaxBytes {
		return in, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	value := func(v any) func() error { return func() error { return d.Decode(v) } }
	r := &in.Resource
	err := object(d, map[string]func() error{
		"version":    value(&in.Version),
		"link_url":   value(&in.LinkURL),
		"credential": value(&in.Credential),
		"resource": func() error {
			return object(d, map[string]func() error{
				"tenant_id":      value(&r.TenantID),
				"environment_id": value(&r.EnvironmentID),
				"kind":           value(&r.Kind),
				"id":             value(&r.ID),
				"generation":     value(&r.Generation),
			})
		},
	})
	if err != nil || d.Decode(new(any)) != io.EOF || in.Validate() != nil {
		return Input{}, ErrInvalid
	}
	return in, nil
}

// object decodes one JSON object whose members are exactly the keys of
// fields, each once.
func object(d *json.Decoder, fields map[string]func() error) error {
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || fields[key] == nil {
			return ErrInvalid
		}
		seen[key] = true
		if fields[key]() != nil {
			return ErrInvalid
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || len(seen) != len(fields) {
		return ErrInvalid
	}
	return nil
}
