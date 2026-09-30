package claudecode

import (
	"bytes"
	"encoding/json"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"testing"
)

func TestOrderedMessageInputNativeConversion(t *testing.T) {
	image := "data:image/png;base64,aW1hZ2U="
	input := proto.TextInput(" before ")
	input[0].Content = append(input[0].Content, proto.InputContent{Type: "input_image", ImageURL: &image}, proto.TextInput(" after ")[0].Content[0])
	input = append(input, proto.TextInput("next")...)
	raw, err := buildOrderedUserMessages(input)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("message boundary lost: %s", raw)
	}
	var first struct {
		Message struct{ Content []userContentBlock }
	}
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatal(err)
	}
	content := first.Message.Content
	if len(content) != 3 || content[0].Text != " before " || content[1].Source == nil || content[1].Source.Data != "aW1hZ2U=" || content[2].Text != " after " {
		t.Fatalf("native order changed: %s", raw)
	}
	image = "https://unsupported.example/image.png"
	if _, err := buildOrderedUserMessages(input); err == nil {
		t.Fatal("unsupported image reference accepted")
	}
	if _, err := buildOrderedUserMessages(proto.TextInput("")); err == nil {
		t.Fatal("empty message accepted")
	}
}
