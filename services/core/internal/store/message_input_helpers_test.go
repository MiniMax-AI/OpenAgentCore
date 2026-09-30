package store_test

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"testing"
)

func inputTextForTest(t *testing.T, input proto.MessageInput) string {
	t.Helper()
	text, err := input.TextOnly()
	if err != nil {
		t.Fatal(err)
	}
	return text
}
