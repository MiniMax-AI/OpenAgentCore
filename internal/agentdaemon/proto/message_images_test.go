package proto

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestInlineMessageImages(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		var encoded bytes.Buffer
		picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
		if format == "png" {
			_ = png.Encode(&encoded, picture)
		} else {
			_ = jpeg.Encode(&encoded, picture, nil)
		}
		url := "data:image/" + format + ";base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
		input := MessageInput{{Content: []InputContent{{Type: "input_image", ImageURL: &url}}}}
		if err := input.ValidateInlineImages(); err != nil {
			t.Fatal(format, err)
		}
		wrongType := "data:image/gif;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
		input[0].Content[0].ImageURL = &wrongType
		if input.ValidateInlineImages() == nil {
			t.Fatal("accepted unsupported image type")
		}
	}
	for _, url := range []string{"https://example.test/image.png", "file:///private/image.png", "data:image/png;base64,AA==", "data:image/png;base64,!", "data:image/png;base64,"} {
		input := MessageInput{{Content: []InputContent{{Type: "input_image", ImageURL: &url}}}}
		if input.ValidateInlineImages() == nil {
			t.Fatalf("accepted %q", url)
		}
		// User-message qualification must not narrow the function-output union.
		if input.Validate() != nil {
			t.Fatalf("inline profile leaked into the shared content union: %q", url)
		}
	}
	if err := TextInput("ordinary text").ValidateInlineImages(); err != nil {
		t.Fatal(err)
	}
}
