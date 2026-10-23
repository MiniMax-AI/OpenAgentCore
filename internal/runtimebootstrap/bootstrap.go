// Package runtimebootstrap owns the Provider-to-Runtime connection input.
// Providers deliver this document as a private file; only Runtime interprets it.
package runtimebootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

const Version = 3
const MaxBytes = 16 * 1024

// PublicWorkspace is the workspace path applications and Core use for a hosted Environment.
// The Runtime maps it to its bound workspace directory.
const PublicWorkspace = "/workspace"

// RuntimeWorkspace is the workspace directory of the packaged Runtime layout.
const RuntimeWorkspace = "/environment/workspace"

// SuspendControlFile is where the Runtime daemon publishes its suspend control.
// Its directory is private to the Runtime user. The Runtime publishes it exactly when
// the bootstrap sets SuspendControl, and Core's wake command reads it.
const SuspendControlFile = "/run/oac/daemon-suspend.json"

var ErrInvalid = errors.New("invalid Runtime bootstrap input")

// Connection is a current-version launch input, not Runtime's private auth store.
// CoreURL includes the machine API base path. The provider owns delivery and
// file permissions; Runtime owns validation and its authentication representation.
// EnvironmentID, SessionID and Workspace bind the Runtime to its Environment.
// SuspendControl is true exactly when the Provider declares checkpoint support.
type Connection struct {
	Version        int    `json:"version"`
	CoreURL        string `json:"core_url"`
	DeviceID       string `json:"device_id"`
	Credential     string `json:"credential"`
	EnvironmentID  string `json:"environment_id"`
	SessionID      string `json:"session_id"`
	Workspace      string `json:"workspace"`
	SuspendControl bool   `json:"suspend_control"`
}

func canonicalUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func (c Connection) Validate() error {
	u, err := url.Parse(c.CoreURL)
	if c.Version != Version || err != nil || (u.Scheme != "https" && u.Scheme != "http") ||
		u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		u.RawPath != "" || u.Path != "/api/v1" ||
		strings.ContainsAny(c.CoreURL, "?#") || strings.ContainsFunc(c.CoreURL, unicode.IsSpace) ||
		!canonicalUUID(c.DeviceID) || !canonicalUUID(c.EnvironmentID) || !canonicalUUID(c.SessionID) ||
		!filepath.IsAbs(c.Workspace) || filepath.Clean(c.Workspace) != c.Workspace || c.Workspace == "/" ||
		c.Credential == "" || strings.ContainsFunc(c.Credential, func(r rune) bool { return r == 0 || unicode.IsSpace(r) }) {
		return ErrInvalid
	}
	raw, err := json.Marshal(c)
	if err != nil || len(raw) > MaxBytes {
		return ErrInvalid
	}
	return nil
}

func (c Connection) Marshal() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(c)
}

// Decode accepts one exact object. Unknown, duplicate, missing and case-aliased
// fields reject instead of introducing alternate spellings of this contract.
func Decode(raw []byte) (Connection, error) {
	var c Connection
	if len(raw) > MaxBytes {
		return c, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return c, ErrInvalid
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return Connection{}, ErrInvalid
		}
		seen[key] = true
		switch key {
		case "version":
			err = d.Decode(&c.Version)
		case "core_url":
			err = d.Decode(&c.CoreURL)
		case "device_id":
			err = d.Decode(&c.DeviceID)
		case "credential":
			err = d.Decode(&c.Credential)
		case "environment_id":
			err = d.Decode(&c.EnvironmentID)
		case "session_id":
			err = d.Decode(&c.SessionID)
		case "workspace":
			err = d.Decode(&c.Workspace)
		case "suspend_control":
			var enabled *bool
			if err = d.Decode(&enabled); err == nil && enabled == nil {
				err = ErrInvalid
			}
			if enabled != nil {
				c.SuspendControl = *enabled
			}
		default:
			return Connection{}, ErrInvalid
		}
		if err != nil {
			return Connection{}, ErrInvalid
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return Connection{}, ErrInvalid
	}
	if len(seen) != 8 || d.Decode(new(any)) != io.EOF || c.Validate() != nil {
		return Connection{}, ErrInvalid
	}
	return c, nil
}
