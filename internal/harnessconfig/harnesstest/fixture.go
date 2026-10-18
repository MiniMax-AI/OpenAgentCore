// Package harnesstest supplies explicit Harness declarations for admission fixtures.
package harnesstest

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

// Declaration supports only common text admission in the "none" placement by
// default: every capability is unsupported, with the official default medium
// verbosity that Core always sends. change adjusts the result.
func Declaration(kind string, change func(*harnessconfig.Declaration)) harnessconfig.Declaration {
	d := harnessconfig.Declaration{
		Kind:               kind,
		Label:              kind,
		Capabilities:       proto.UnsupportedCapabilities(),
		Execution:          harnessconfig.ExecutionOptions{TextVerbosities: []string{"medium"}},
		Placements:         []string{"none"},
		MCPOrigins:         []string{},
		WhitespaceOnlyText: proto.CapabilityUnsupported,
	}
	if change != nil {
		change(&d)
	}
	return d
}
