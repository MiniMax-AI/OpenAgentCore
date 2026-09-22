package codex

import (
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"testing"
)

func TestNativeInputRetainsImageOrderAndMessageSeparator(t *testing.T) {
	image := "data:image/png;base64,aW1hZ2U="
	messages := proto.TextInput(" before ")
	messages[0].Content = append(messages[0].Content, proto.InputContent{Type: "input_image", ImageURL: &image}, proto.TextInput(" after ")[0].Content[0])
	messages = append(messages, proto.TextInput("next")...)
	input, err := nativeInput(messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 5 || input[0].Text != " before " || input[1].Type != UserInputRemoteImg || input[1].URL != image || input[2].Text != " after " || input[3].Text != "\n\n" || input[4].Text != "next" {
		t.Fatalf("native input changed: %+v", input)
	}
}
