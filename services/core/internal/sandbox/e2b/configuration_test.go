package e2b

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestConfigurationCodecSeparatesSecretsAndPreservesObservations(t *testing.T) {
	adapter := ConfigurationAdapter{}
	for _, metadata := range []string{`{}`, `{"template_build":{"status":null,"resources":{"cpus":2,"memory_mib":null,"root_disk_mib":null}}}`, `{"template_build":{"status":"ready","resources":{"cpus":2,"memory_mib":2048,"root_disk_mib":null}}}`} {
		record := sandbox.ConfigurationRecord{Public: json.RawMessage(`{"template":"old-build","api_url":"https://api.e2b.app","domain":"e2b.app"}`), Metadata: json.RawMessage(metadata), Secret: []byte("private-fixture")}
		restored, err := adapter.Decode(record)
		if err != nil {
			t.Fatal(err)
		}
		// Retained ownership is readable even when its old selector would fail new admission.
		if _, err := adapter.Normalize(sandbox.Selection{Provider: "e2b", Configuration: restored}); !errors.Is(err, sandbox.ErrInvalid) {
			t.Fatal("old selector admitted", err)
		}
		encoded, err := adapter.Encode(restored)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(encoded.Secret, record.Secret) {
			t.Fatal("secret changed")
		}
		if len(encoded.Metadata) == 0 {
			encoded.Metadata = json.RawMessage(`{}`)
		}
		if string(encoded.Metadata) != metadata {
			t.Fatalf("observation changed: %s", encoded.Metadata)
		}
		values := []any{restored, struct{ Nested any }{restored}, sandbox.Selection{Configuration: restored}, encoded}
		for _, v := range values {
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, record.Secret) {
				t.Fatal("secret serialized")
			}
		}
		if bytes.Contains([]byte(fmt.Sprintf("%+v", restored)), record.Secret) {
			t.Fatal("secret in diagnostic")
		}
	}
}

func TestConfigurationInputRejectsUnknownAndPrivateFields(t *testing.T) {
	adapter := ConfigurationAdapter{}
	for _, tc := range []struct{ public, secret string }{
		{`null`, ``}, {`[]`, ``}, {`{"api_key":"private"}`, ``}, {`{"Template":"x"}`, ``},
		{`{"template_build":{}}`, ``}, {`{}`, `null`}, {`{}`, `{"api_key":null}`}, {`{}`, `{"api_key":""}`},
		{`{}`, `{"API_KEY":"private"}`}, {`{}`, `{"api_key":"private","extra":true}`},
	} {
		if _, err := adapter.DecodeInput(json.RawMessage(tc.public), json.RawMessage(tc.secret)); !errors.Is(err, sandbox.ErrInvalid) {
			t.Fatalf("input accepted: %s", tc.public)
		}
	}
	c, err := adapter.DecodeInput(json.RawMessage(`{"template":"test"}`), nil)
	if err != nil || c.ReplacesCredential() {
		t.Fatal("omission lost")
	}
	c, err = adapter.DecodeInput(json.RawMessage(`{"template":"test"}`), json.RawMessage(`{"api_key":"private"}`))
	if err != nil || !c.ReplacesCredential() {
		t.Fatal("explicit replacement lost")
	}
}
