package proto

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMessageInputOrderAndTextOnlyRejection(t *testing.T) {
	raw := []byte(`[{"content":[{"type":"input_text","text":" before "},{"type":"input_image","image_url":"data:image/png;base64,aW1hZ2U="},{"type":"input_text","text":" after "}]},{"content":[{"type":"input_text","text":"next"}]}]`)
	var input MessageInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	if err := input.Validate(); err != nil {
		t.Fatal(err)
	}
	if !input.HasImages() {
		t.Fatal("image lost")
	}
	if _, err := input.TextOnly(); err == nil {
		t.Fatal("text-only adapter silently accepted image")
	}
	encoded, err := json.Marshal(input)
	if err != nil || string(encoded) != string(raw) {
		t.Fatalf("content changed: %s, %v", encoded, err)
	}
	text := append(TextInput(" before "), TextInput(" after ")...)
	actual, err := text.TextOnly()
	if err != nil || actual != " before \n\n after " {
		t.Fatalf("text changed: %q %v", actual, err)
	}
	for _, payload := range []any{PromptRequestPayload{Input: input}, PromptSteerPayload{Input: input}, ExecutionStartPayload{Input: input}} {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Input MessageInput `json:"input"`
		}
		if err := json.Unmarshal(encoded, &wire); err != nil || !reflect.DeepEqual(input, wire.Input) {
			t.Fatalf("wire loses input: %s", encoded)
		}
	}
}

func TestMessageInputInvalidUnion(t *testing.T) {
	for _, raw := range []string{`[]`, `[{"content":[]}]`, `[{"content":[{"type":"input_text","text":null}]}]`, `[{"content":[{"type":"input_image","image_url":""}]}]`, `[{"content":[{"type":"input_image","image_url":"image","text":"unexpected"}]}]`} {
		var input MessageInput
		if err := json.Unmarshal([]byte(raw), &input); err == nil && input.Validate() == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
