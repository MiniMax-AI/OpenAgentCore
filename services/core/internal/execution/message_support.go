package execution

import (
	"errors"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// ErrWhitespaceOnlyText is a declared native limitation reported before any
// write, reservation or promotion.
var ErrWhitespaceOnlyText = errors.New("whitespace-only message text is not supported by this harness")

// validateMessageTextProfile rejects a message without an image or any
// non-whitespace text when the harness has not qualified such input.
func validateMessageTextProfile(profile engine.Profile, input proto.MessageInput) error {
	if profile.WhitespaceOnlyText.IsSupported() {
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

// blankTextRune is the explicit union of Go unicode.IsSpace and ECMAScript
// String.prototype.trim (WhiteSpace and LineTerminator). The Claude bridge uses
// the same set, so admission rejects every message the bridge would reject.
func blankTextRune(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u0085', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return r >= '\u2000' && r <= '\u200a'
}

func validateMessageImageProfile(profile engine.Profile, _ string, input proto.MessageInput) error {
	if !input.HasImages() {
		return nil
	}
	if !profile.MessageImages.IsSupported() || input.ValidateInlineImages() != nil {
		return store.ErrInvalidInput
	}
	return nil
}

// Image support is checked only for the operation that actually carries images.
func (p Policy) messageInputSupport(peer *gateway.Session, kind string, snapshot Snapshot, input proto.MessageInput) error {
	if !input.HasImages() {
		return nil
	}
	profile, ok := p.Engines.Lookup(kind)
	if !ok {
		return store.ErrInvalidInput
	}
	placement := ""
	if snapshot.Environment != nil {
		placement = snapshot.Environment.Type
	}
	if err := validateMessageImageProfile(profile, placement, input); err != nil {
		return err
	}
	return requireMessageImages(peer, kind, input)
}

func requireMessageImages(peer *gateway.Session, kind string, input proto.MessageInput) error {
	if !input.HasImages() {
		return nil
	}
	info, found, known := peer.AgentKindStatus(kind)
	if !found || !known || !info.Available || !info.Capabilities.MessageImages {
		return errors.New("Runtime does not support message images")
	}
	return nil
}
