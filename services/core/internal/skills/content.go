package skills

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
)

// VerifyContent checks that a decrypted archive is still the complete bundle
// its version records, before anything reads or installs it.
func VerifyContent(content Content) error {
	metadata := proto.SkillMetadata{Type: "inline", Name: content.Version.Name, Description: content.Version.Description}
	if _, err := agentskill.Read(content.Archive, metadata); err != nil {
		return ErrInvalidInput
	}
	return nil
}
