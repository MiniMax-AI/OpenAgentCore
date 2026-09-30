package execution

import (
	"encoding/json"
	"testing"
)

func TestMessageInputRetainsPublicBoundariesAndImages(t *testing.T) {
	input, err := messageInput(json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":" before "},{"type":"input_image","image_url":"data:image/png;base64,aW1hZ2U="},{"type":"input_text","text":" after "}]},{"role":"user","content":[{"type":"input_text","text":"next"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 2 || len(input[0].Content) != 3 || *input[0].Content[0].Text != " before " || *input[0].Content[1].ImageURL != "data:image/png;base64,aW1hZ2U=" || *input[0].Content[2].Text != " after " || *input[1].Content[0].Text != "next" {
		t.Fatalf("message input changed: %+v", input)
	}
}
