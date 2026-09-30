package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"strings"
)

func buildOrderedUserMessages(input proto.MessageInput) ([]byte, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	for _, message := range input {
		var blocks []userContentBlock
		for _, part := range message.Content {
			if part.Type == "input_text" {
				blocks = append(blocks, userContentBlock{Type: "text", Text: *part.Text})
				continue
			}
			header, data, ok := strings.Cut(*part.ImageURL, ",")
			if !ok || (header != "data:image/png;base64" && header != "data:image/jpeg;base64") {
				return nil, fmt.Errorf("claudecode: unsupported image reference")
			}
			mime := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
			blocks = append(blocks, userContentBlock{Type: "image", Source: &userContentSource{Type: "base64", MediaType: mime, Data: data}})
		}
		var content any = blocks
		if len(blocks) == 1 && blocks[0].Type == "text" {
			content = blocks[0].Text
		}
		if err := encoder.Encode(userMessage{Type: "user", Message: userMessageContent{Role: "user", Content: content}}); err != nil {
			return nil, err
		}
	}
	return output.Bytes(), nil
}
