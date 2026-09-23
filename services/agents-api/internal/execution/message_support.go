package execution

import (
	"errors"
	"slices"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// ErrWhitespaceOnlyText is a declared native limitation reported before any
// write, reservation or promotion.
var ErrWhitespaceOnlyText = errors.New("whitespace-only message text is not supported by this harness")

// validateMessageTextProfile rejects a message without an image or any
// non-whitespace text when the harness has not qualified such input.
func validateMessageTextProfile(profile engine.Profile, input proto.MessageInput) error {
	if profile.WhitespaceOnlyText {
		return nil
	}
	for _, message := range input {
		meaningful := false
		for _, part := range message.Content {
			if part.Type == "input_image" || (part.Text != nil && strings.TrimSpace(*part.Text) != "") {
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

func validateMessageImageProfile(profile engine.Profile, placement string, input proto.MessageInput) error {
	if !input.HasImages() {
		return nil
	}
	if !slices.Contains(profile.MessageImagePlacements, placement) || input.ValidateInlineImages() != nil {
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
