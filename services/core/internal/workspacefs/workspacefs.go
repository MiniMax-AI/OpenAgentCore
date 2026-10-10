// Package workspacefs defines the independent workspace filesystem boundary.
// Its owning document is docs/workspace-provider.md. Core owns identities and
// lifecycle; adapters own filesystem configuration, receipts and path resolution.
package workspacefs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrInvalid     = errors.New("invalid workspace filesystem request")
	ErrOwnership   = errors.New("workspace filesystem ownership mismatch")
	ErrUnavailable = errors.New("workspace filesystem unavailable")
	ErrUnconfirmed = errors.New("workspace filesystem mutation outcome unconfirmed")
	ErrNotFound    = errors.New("workspace filesystem object not found")
	ErrUnsupported = errors.New("unsupported workspace filesystem combination")
)

// Reference is persisted before Create. Its canonical UUIDs are immutable and
// ObjectID is never reused, including after deletion or an unconfirmed mutation.
type Reference struct {
	TenantID      string `json:"tenant_id"`
	EnvironmentID string `json:"environment_id"`
	ObjectID      string `json:"object_id"`
}

// Configuration is an immutable Core database record. Parameters belong to its
// adapter; neither Core nor the public Session API interprets native paths.
type Configuration struct {
	ID         string          `json:"id" format:"uuid" binding:"required"`
	Adapter    string          `json:"adapter" binding:"required"`
	Parameters json.RawMessage `json:"parameters" swaggertype:"object" binding:"required"`
}

type AttachmentKind string

const AttachmentHostDirectory AttachmentKind = "host_directory"

// Attachment is adapter-issued ownership evidence, not a public host path.
// The selected adapter must validate Native before using it.
type Attachment struct {
	Reference       Reference       `json:"reference"`
	ConfigurationID string          `json:"configuration_id"`
	Kind            AttachmentKind  `json:"kind"`
	Native          json.RawMessage `json:"native"`
}

// Binding transports the database-owned configuration transiently to a resolver;
// it must not become an independently authored node setting.
type Binding struct {
	Configuration Configuration `json:"configuration"`
	Attachment    Attachment    `json:"attachment"`
}

type Directory struct {
	Path string `json:"path"`
}

// Declaration describes verified behavior. CapacityQuota means the adapter
// enforces the requested capacity, not merely that it can report free space.
type Declaration struct {
	Attachment    AttachmentKind `json:"attachment" binding:"required"`
	UserXAttr     bool           `json:"user_xattr" binding:"required"`
	CapacityQuota bool           `json:"capacity_quota" binding:"required"`
}

type Requirements struct {
	Attachment AttachmentKind `json:"attachment"`
	UserXAttr  bool           `json:"user_xattr"`
}

// Control is bound to one immutable Configuration. All operations are mandatory.
// Errors retain ownership; context cancellation does not prove mutation settlement.
// Delete requires explicit Session deletion and confirmed compute stop by Core.
type Control interface {
	Declaration() Declaration
	Check(context.Context) error
	// Create retries and concurrent calls for the same immutable Reference converge
	// on one object without overwriting its data, including after ErrUnconfirmed.
	// A terminally deleted Reference must never be recreated.
	Create(context.Context, Reference) (Attachment, error)
	// Observe returns ErrNotFound for absence, which does not settle a mutation.
	Observe(context.Context, Reference) (Attachment, error)
	// Delete retries and concurrent calls for the same Reference converge on
	// terminal deletion. Concurrent or late Create cannot resurrect the object.
	Delete(context.Context, Reference) error
}

// Resolver validates ownership and adapter-native data before exposing a local
// directory to compute. Core forwards bindings without constructing paths.
type Resolver interface {
	Resolve(context.Context, Binding) (Directory, error)
}

func canonicalUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func (r Reference) Validate() error {
	if !canonicalUUID(r.TenantID) || !canonicalUUID(r.EnvironmentID) || !canonicalUUID(r.ObjectID) {
		return fmt.Errorf("%w: reference requires canonical nonzero UUIDs", ErrInvalid)
	}
	return nil
}

func (c Configuration) Validate() error {
	if !canonicalUUID(c.ID) || c.Adapter == "" || strings.TrimSpace(c.Adapter) != c.Adapter || strings.ContainsAny(c.Adapter, "/\\\x00\r\n\t") {
		return fmt.Errorf("%w: configuration identity or adapter", ErrInvalid)
	}
	if !jsonObject(c.Parameters) {
		return fmt.Errorf("%w: configuration parameters must be a JSON object", ErrInvalid)
	}
	return nil
}

func (a Attachment) Validate() error {
	if err := a.Reference.Validate(); err != nil {
		return err
	}
	if !canonicalUUID(a.ConfigurationID) || a.Kind != AttachmentHostDirectory || !jsonObject(a.Native) {
		return fmt.Errorf("%w: attachment envelope", ErrInvalid)
	}
	return nil
}

// ValidateAttachment checks the shared envelope only. It cannot establish native
// ownership: the adapter must validate its parameters, receipt and owned resource.
func ValidateAttachment(reference Reference, configuration Configuration, attachment Attachment) error {
	if err := reference.Validate(); err != nil {
		return err
	}
	if err := configuration.Validate(); err != nil {
		return err
	}
	if err := attachment.Validate(); err != nil {
		return err
	}
	if attachment.Reference != reference || attachment.ConfigurationID != configuration.ID {
		return fmt.Errorf("%w: attachment reference or configuration", ErrOwnership)
	}
	return nil
}

func (b Binding) Validate() error {
	return ValidateAttachment(b.Attachment.Reference, b.Configuration, b.Attachment)
}

func (d Directory) Validate() error {
	if !filepath.IsAbs(d.Path) || strings.ContainsRune(d.Path, '\x00') || filepath.Clean(d.Path) != d.Path {
		return fmt.Errorf("%w: resolved directory must be a clean absolute path", ErrInvalid)
	}
	return nil
}

// ValidateCombination runs before any allocation. Zero capacity requests no quota;
// a positive request requires actual quota enforcement by the selected adapter.
func ValidateCombination(requirements Requirements, declaration Declaration, requestedCapacityMiB uint32) error {
	if requirements.Attachment != AttachmentHostDirectory || declaration.Attachment != AttachmentHostDirectory || requirements.Attachment != declaration.Attachment {
		return fmt.Errorf("%w: attachment kind", ErrUnsupported)
	}
	if requirements.UserXAttr && !declaration.UserXAttr {
		return fmt.Errorf("%w: user_xattr", ErrUnsupported)
	}
	if requestedCapacityMiB > 0 && !declaration.CapacityQuota {
		return fmt.Errorf("%w: capacity quota is not enforced", ErrUnsupported)
	}
	return nil
}

func jsonObject(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{' && json.Valid(raw)
}
