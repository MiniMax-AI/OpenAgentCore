package codex

import "github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"

// nativeInput keeps content order. Codex accepts a flat input list, so message
// boundaries use the same blank-line separator as the existing text path.
func nativeInput(messages proto.MessageInput) ([]UserInput, error) {
	if err := messages.Validate(); err != nil {
		return nil, err
	}
	var input []UserInput
	for i, message := range messages {
		if i > 0 {
			input = append(input, UserInput{Type: UserInputText, Text: "\n\n"})
		}
		for _, part := range message.Content {
			if part.Type == "input_text" {
				input = append(input, UserInput{Type: UserInputText, Text: *part.Text})
			} else {
				input = append(input, UserInput{Type: UserInputRemoteImg, URL: *part.ImageURL})
			}
		}
	}
	return input, nil
}
