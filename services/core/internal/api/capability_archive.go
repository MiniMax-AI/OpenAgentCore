package api

import (
	"encoding/base64"
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentbundle"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// Skills and Plugins share the pinned inline capability source shape.
func decodeCapabilityArchive(raw json.RawMessage) ([]byte, error) {
	var source struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	}
	if decodeInputObject(raw, &source, "type", "media_type", "data") != nil || source.Type != "base64" || source.MediaType != "application/zip" || len(source.Data) > base64.StdEncoding.EncodedLen(agentbundle.MaxArchiveBytes) {
		return nil, sessions.ErrInvalidInput
	}
	body, err := base64.StdEncoding.Strict().DecodeString(source.Data)
	if err != nil {
		return nil, sessions.ErrInvalidInput
	}
	return body, nil
}
