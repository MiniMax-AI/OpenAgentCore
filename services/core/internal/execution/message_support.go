package execution

import (
	"errors"
	"strings"
	"unicode"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// ErrWhitespaceOnlyText is a declared native limitation reported before any
// write, reservation or promotion.
var ErrWhitespaceOnlyText = errors.New("whitespace-only message text is not supported by this harness")

// validateMessageText rejects a message without an image or any non-whitespace
// text when the Harness does not declare support for such input.
func validateMessageText(declaration harnessconfig.Declaration, input proto.MessageInput) error {
	if declaration.WhitespaceOnlyText.IsSupported() {
		return nil
	}
	for _, message := range input {
		meaningful := false
		for _, part := range message.Content {
			if part.Type == "input_image" || (part.Text != nil && strings.TrimFunc(*part.Text, blankTextRune) != "") {
				meaningful = true
				break
			}
		}
		if !meaningful {
			return ErrWhitespaceOnlyText
		}
	}
	return nil
}

// blankTextRune is Go whitespace plus the byte order mark, so the byte order mark alone does not count as text.
func blankTextRune(r rune) bool { return unicode.IsSpace(r) || r == '\ufeff' }

func validateMessageImages(declaration harnessconfig.Declaration, input proto.MessageInput) error {
	if !input.HasImages() {
		return nil
	}
	if !declaration.Capabilities.MessageImages.IsSupported() {
		return sessions.ErrInvalidInput
	}
	if declaration.ValidateMessageInput != nil {
		return admissionError(declaration.ValidateMessageInput(input))
	}
	return nil
}

// Image support is checked only for the operation that actually carries images.
func (p Policy) messageInputSupport(peer *runtimegateway.Session, kind string, snapshot Snapshot, input proto.MessageInput) error {
	if !input.HasImages() {
		return nil
	}
	declaration, ok := p.Harnesses().Lookup(kind)
	if !ok {
		return sessions.ErrInvalidInput
	}
	if err := validateMessageImages(declaration, input); err != nil {
		return err
	}
	return requireMessageImages(peer, kind, input)
}

func requireMessageImages(peer *runtimegateway.Session, kind string, input proto.MessageInput) error {
	if !input.HasImages() {
		return nil
	}
	info, found, known := peer.AgentKindStatus(kind)
	if !found || !known || !info.Available || !info.Capabilities.MessageImages.IsSupported() {
		return errors.New("Runtime does not support message images")
	}
	return nil
}
