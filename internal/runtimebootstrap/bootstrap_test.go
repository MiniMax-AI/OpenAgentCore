package runtimebootstrap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConnectionRoundTrip(t *testing.T) {
	input := Connection{Version: Version, CoreURL: "https://core.example/api/v1", DeviceID: "da912024-1543-4242-a2c1-5f4f7ebbc6c7", Credential: "test-credential"}
	raw, err := input.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(raw)
	if err != nil || got != input {
		t.Fatal("connection changed in transit", err)
	}
}

func TestConnectionRejectsAmbiguousOrForeignInput(t *testing.T) {
	good := `{"version":1,"core_url":"https://core.example/api/v1","device_id":"da912024-1543-4242-a2c1-5f4f7ebbc6c7","credential":"test-secret"}`
	for name, raw := range map[string]string{
		"null": "null", "array": "[]", "trailing": good + "{}",
		"duplicate": strings.Replace(good, `"version":1`, `"version":1,"version":1`, 1),
		"unknown":   strings.Replace(good, `"version":1`, `"extra":1,"version":1`, 1),
		"case":      strings.Replace(good, "credential", "Credential", 1),
		"old auth":  `{"server_url":"https://core.example","runtime_id":"old","runner_credential":"test-secret"}`,
		"oversized": strings.Repeat(" ", MaxBytes) + good,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode([]byte(raw)); err != ErrInvalid {
				t.Fatal("accepted invalid input")
			}
		})
	}
	for field, values := range map[string][]any{
		"version":    {nil, 0, 2, "1"},
		"core_url":   {"http://user:pass@core.example/api/v1", "https://core.example/api/v1?", "https://core.example/api/v1#", "https://core.example", "https://core.example/api/v1/", "https://core.example/api/v1?q=1", ""},
		"device_id":  {"", "invalid", "00000000-0000-0000-0000-000000000000"},
		"credential": {nil, "", "test\nsecret", "test\x00secret"},
	} {
		for _, value := range values {
			var input map[string]any
			_ = json.Unmarshal([]byte(good), &input)
			input[field] = value
			raw, _ := json.Marshal(input)
			if _, err := Decode(raw); err != ErrInvalid {
				t.Fatalf("accepted invalid %s", field)
			}
		}
		var input map[string]any
		_ = json.Unmarshal([]byte(good), &input)
		delete(input, field)
		raw, _ := json.Marshal(input)
		if _, err := Decode(raw); err != ErrInvalid {
			t.Fatalf("accepted missing %s", field)
		}
	}
}
