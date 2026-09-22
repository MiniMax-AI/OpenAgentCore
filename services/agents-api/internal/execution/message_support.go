package execution

import (
	"errors"
	"slices"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

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
