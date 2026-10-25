package runtimebootstrap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConnectionRoundTrip(t *testing.T) {
	input := Connection{Version: Version, CoreURL: "https://core.example/api/v1", DeviceID: "da912024-1543-4242-a2c1-5f4f7ebbc6c7", Credential: "test-credential",
		EnvironmentID: "0d1f4f1c-6c3e-4e0b-9a52-3e3d8b0e7a11", SessionID: "6a1b7c0e-2f4d-4c55-8f6e-9d2a1b3c4d5e", Workspace: RuntimeWorkspace, SuspendControl: true}
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
	good := `{"version":3,"core_url":"https://core.example/api/v1","device_id":"da912024-1543-4242-a2c1-5f4f7ebbc6c7","credential":"test-secret","environment_id":"0d1f4f1c-6c3e-4e0b-9a52-3e3d8b0e7a11","session_id":"6a1b7c0e-2f4d-4c55-8f6e-9d2a1b3c4d5e","workspace":"/environment/workspace","suspend_control":false}`
	if _, err := Decode([]byte(good)); err != nil {
		t.Fatal("valid input rejected", err)
	}
	for name, raw := range map[string]string{
		"null": "null", "array": "[]", "trailing": good + "{}",
		"duplicate": strings.Replace(good, `"version":3`, `"version":3,"version":3`, 1),
		"unknown":   strings.Replace(good, `"version":3`, `"extra":1,"version":3`, 1),
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
		"version":         {nil, 0, 2, "3"},
		"suspend_control": {nil, 1, "true"},
		"core_url":        {"http://user:pass@core.example/api/v1", "https://core.example/api/v1?", "https://core.example/api/v1#", "https://core.example", "https://core.example/api/v1/", "https://core.example/api/v1?q=1", ""},
		"device_id":       {"", "invalid", "00000000-0000-0000-0000-000000000000"},
		"credential":      {nil, "", "test\nsecret", "test\x00secret"},
		"environment_id":  {"", "invalid", "00000000-0000-0000-0000-000000000000"},
		"session_id":      {"", "invalid", "00000000-0000-0000-0000-000000000000"},
		"workspace":       {"", "workspace", "/", "/environment/../workspace", "/environment/workspace/"},
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
