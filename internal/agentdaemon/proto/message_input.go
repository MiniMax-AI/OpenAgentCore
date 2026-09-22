package proto

import (
	"errors"
	"strings"
)

// InputContent is an ordered text or image part, shared by user and tool input.
// ImageURL is passed unchanged; native encoding belongs to the adapter.
type InputContent struct {
	Type     string  `json:"type"`
	Text     *string `json:"text,omitempty"`
	ImageURL *string `json:"image_url,omitempty"`
}

func (p InputContent) Validate() error {
	switch p.Type {
	case "input_text":
		if p.Text != nil && p.ImageURL == nil {
			return nil
		}
	case "input_image":
		if p.ImageURL != nil && p.Text == nil {
			return nil
		}
	}
	return errors.New("input requires text or image content")
}

// MessageInput preserves user-message boundaries and content order on every
// Runtime input path. Adapters map these messages to their native input format.
type MessageInput []InputMessage

type InputMessage struct {
	Content []InputContent `json:"content"`
}

// TextInput constructs one message without normalizing its text.
func TextInput(text string) MessageInput {
	return MessageInput{{Content: []InputContent{{Type: "input_text", Text: &text}}}}
}

func (m MessageInput) Validate() error {
	if len(m) == 0 {
		return errors.New("user input requires messages")
	}
	for _, message := range m {
		meaningful := false
		for _, part := range message.Content {
			if err := part.Validate(); err != nil {
				return err
			}
			if part.Type == "input_image" {
				if strings.TrimSpace(*part.ImageURL) == "" {
					return errors.New("image input requires a reference")
				}
				meaningful = true
			} else if strings.TrimSpace(*part.Text) != "" {
				meaningful = true
			}
		}
		if !meaningful {
			return errors.New("user message requires content")
		}
	}
	return nil
}

func (m MessageInput) HasImages() bool {
	for _, message := range m {
		for _, part := range message.Content {
			if part.Type == "input_image" {
				return true
			}
		}
	}
	return false
}

// TextOnly is for text-only native transports. It rejects images rather than
// dropping them, and retains the existing blank-line boundary between messages.
func (m MessageInput) TextOnly() (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	var text strings.Builder
	for i, message := range m {
		if i > 0 {
			text.WriteString("\n\n")
		}
		for _, part := range message.Content {
			if part.Type != "input_text" {
				return "", errors.New("native transport does not support image input")
			}
			text.WriteString(*part.Text)
		}
	}
	return text.String(), nil
}
