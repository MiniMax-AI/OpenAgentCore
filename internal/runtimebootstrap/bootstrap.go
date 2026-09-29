// Package runtimebootstrap owns the Provider-to-Runtime connection input.
// Providers deliver this document as a private file; only Runtime interprets it.
package runtimebootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

const Version = 1
const MaxBytes = 16 * 1024

var ErrInvalid = errors.New("invalid Runtime bootstrap input")

// Connection is a current-version launch input, not Runtime's private auth store.
// CoreURL includes the machine API base path. The provider owns delivery and
// file permissions; Runtime owns validation and its authentication representation.
type Connection struct {
	Version    int    `json:"version"`
	CoreURL    string `json:"core_url"`
	DeviceID   string `json:"device_id"`
	Credential string `json:"credential"`
}

func (c Connection) Validate() error {
	u, err := url.Parse(c.CoreURL)
	id, idErr := uuid.Parse(c.DeviceID)
	if c.Version != Version || err != nil || (u.Scheme != "https" && u.Scheme != "http") ||
		u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		u.RawPath != "" || u.Path != "/api/v1" ||
		strings.ContainsAny(c.CoreURL, "?#") || strings.ContainsFunc(c.CoreURL, unicode.IsSpace) ||
		idErr != nil || id == uuid.Nil || id.String() != c.DeviceID ||
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
	if len(seen) != 4 || d.Decode(new(any)) != io.EOF || c.Validate() != nil {
		return Connection{}, ErrInvalid
	}
	return c, nil
}
