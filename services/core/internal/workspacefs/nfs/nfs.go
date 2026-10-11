// Package nfs implements workspace storage on an operator-mounted kernel NFS filesystem.
package nfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/google/uuid"
)

const Name = "nfs"

type parameters struct {
	Root        string           `json:"root"`
	NamespaceID string           `json:"namespace_id"`
	UID         uint32           `json:"uid"`
	Quota       *quotaParameters `json:"quota,omitempty"`
}

// CanonicalParameters rejects unknown, duplicate, missing and malformed settings.
func CanonicalParameters(raw json.RawMessage) (json.RawMessage, error) {
	var p parameters
	if err := strict(raw, &p); err != nil {
		return nil, fmt.Errorf("%w: parameters: %v", workspacefs.ErrInvalid, err)
	}
	id, err := uuid.Parse(p.NamespaceID)
	if err != nil || id == uuid.Nil || id.String() != p.NamespaceID || p.UID == 0 || p.UID > 2147483647 || !filepath.IsAbs(p.Root) || filepath.Clean(p.Root) != p.Root || bytes.IndexByte([]byte(p.Root), 0) >= 0 {
		return nil, fmt.Errorf("%w: root, namespace_id or uid", workspacefs.ErrInvalid)
	}
	if p.Quota != nil {
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(raw, &fields); err != nil {
			return nil, fmt.Errorf("%w: parameters", workspacefs.ErrInvalid)
		}
		if p.Quota, err = canonicalQuota(fields["quota"]); err != nil {
			return nil, err
		}
	}
	return json.Marshal(p)
}

func strict(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("expected object")
	}
	seen := map[string]bool{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return err
		}
		k, ok := key.(string)
		if !ok || seen[k] {
			return errors.New("duplicate key")
		}
		seen[k] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return err
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("null field")
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	canonical, err := json.Marshal(dst)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(canonical, &fields); err != nil {
		return err
	}
	if len(fields) != len(seen) {
		return errors.New("missing field")
	}
	for field := range seen {
		if _, ok := fields[field]; !ok {
			return errors.New("unknown field")
		}
	}
	return nil
}

// Registration implements every workspacefs.Adapter role for kernel NFS.
type Registration struct{}

func (Registration) Normalize(raw, credential json.RawMessage) (json.RawMessage, []byte, error) {
	canonical, err := CanonicalParameters(raw)
	if err != nil {
		return nil, nil, err
	}
	var p parameters
	_ = json.Unmarshal(canonical, &p)
	encoded, err := encodeCredential(p.Quota, credential)
	if err != nil {
		return nil, nil, err
	}
	return canonical, encoded, nil
}

// Control requires exactly the credential its parameters declare.
func (Registration) Control(record workspacefs.ConfigurationRecord) (workspacefs.Control, error) {
	f, err := newFilesystem(record.Configuration)
	if err != nil {
		return nil, err
	}
	if f.parameters.Quota == nil {
		if len(record.Credential) > 0 {
			return nil, fmt.Errorf("%w: credential without quota parameters", workspacefs.ErrInvalid)
		}
		return f, nil
	}
	if f.quota, err = newQuotaClient(*f.parameters.Quota, record.Credential); err != nil {
		return nil, err
	}
	return f, nil
}

// Resolver never receives a credential; it validates bindings and resolves paths.
func (Registration) Resolver(configuration workspacefs.Configuration) (workspacefs.Resolver, error) {
	return newFilesystem(configuration)
}

// Filesystem holds immutable configuration only; construction performs no I/O.
// quota is set only for a Control constructed with the configured credential.
type Filesystem struct {
	configuration workspacefs.Configuration
	parameters    parameters
	quota         *quotaClient
}

func newFilesystem(configuration workspacefs.Configuration) (*Filesystem, error) {
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	if configuration.Adapter != Name {
		return nil, workspacefs.ErrUnsupported
	}
	raw, err := CanonicalParameters(configuration.Parameters)
	if err != nil {
		return nil, err
	}
	configuration.Parameters = raw
	f := &Filesystem{configuration: configuration}
	_ = json.Unmarshal(raw, &f.parameters)
	return f, nil
}

func (f *Filesystem) Declaration() workspacefs.Declaration {
	return workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true, CapacityQuota: f.parameters.Quota != nil}
}

func unavailable(err error) error { return fmt.Errorf("%w: %w", workspacefs.ErrUnavailable, err) }
func unconfirmed(err error) error { return fmt.Errorf("%w: %w", workspacefs.ErrUnconfirmed, err) }

// Slots remain occupied until the kernel operation actually returns, even after timeout.
var operations = make(chan struct{}, 32)

func run[T any](ctx context.Context, operation func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, fmt.Errorf("%w: %v", workspacefs.ErrUnconfirmed, err)
	}
	select {
	case operations <- struct{}{}:
	default:
		return zero, fmt.Errorf("%w: filesystem operation budget exhausted", workspacefs.ErrUnavailable)
	}
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() { defer func() { <-operations }(); value, err := operation(); done <- result{value, err} }()
	select {
	case r := <-done:
		return r.value, r.err
	case <-ctx.Done():
		return zero, fmt.Errorf("%w: %v", workspacefs.ErrUnconfirmed, ctx.Err())
	}
}

type receipt struct {
	NamespaceID string `json:"namespace_id"`
}

func (f *Filesystem) attachment(ref workspacefs.Reference) workspacefs.Attachment {
	raw, _ := json.Marshal(receipt{f.parameters.NamespaceID})
	return workspacefs.Attachment{Reference: ref, ConfigurationID: f.configuration.ID, Kind: workspacefs.AttachmentHostDirectory, Native: raw}
}
func (f *Filesystem) Resolve(ctx context.Context, b workspacefs.Binding) (workspacefs.Directory, error) {
	if err := b.Validate(); err != nil {
		return workspacefs.Directory{}, err
	}
	raw, err := CanonicalParameters(b.Configuration.Parameters)
	if err != nil {
		return workspacefs.Directory{}, err
	}
	var native receipt
	if b.Configuration.ID != f.configuration.ID || b.Configuration.Adapter != Name || !bytes.Equal(raw, f.configuration.Parameters) || strict(b.Attachment.Native, &native) != nil || native.NamespaceID != f.parameters.NamespaceID {
		return workspacefs.Directory{}, workspacefs.ErrOwnership
	}
	_, err = f.Observe(ctx, b.Attachment.Reference)
	if err != nil {
		return workspacefs.Directory{}, err
	}
	return workspacefs.Directory{Path: filepath.Join(f.parameters.Root, "objects", b.Attachment.Reference.ObjectID, "live", "data")}, nil
}
