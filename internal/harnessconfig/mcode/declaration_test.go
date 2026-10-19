package mcode

import (
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestFunctionSchemaRejectsMCPHeaderKeyword(t *testing.T) {
	for schema, ok := range map[string]bool{
		`{"type":"object","properties":{"q":{"type":"string"}}}`:                         true,
		`{"type":"object","properties":{"q":{"type":"string","x-mcp-header":"Region"}}}`: false,
	} {
		err := Declaration.ValidateTools([]proto.FunctionTool{{Name: "f", Parameters: json.RawMessage(schema)}}, nil)
		if (err == nil) != ok {
			t.Errorf("%s: %v", schema, err)
		}
	}
}
