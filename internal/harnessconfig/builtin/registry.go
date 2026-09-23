// Package builtin composes adapter-owned provider validation rules for Core.
// It contains no native launch configuration or public discovery contract.
package builtin

import (
	"github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig"
	"github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig/claudesdk"
	"github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig/codex"
	"github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig/mcode"
)

var registry = harnessconfig.NewRegistry(map[string]harnessconfig.Configuration{
	"codex":      codex.Configuration(),
	"claude_sdk": claudesdk.Configuration(),
	"mcode":      mcode.Configuration(),
})

func Registry() harnessconfig.Registry { return registry }
