package main

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/processconfig"
)

func testSandboxCapacity(t testing.TB) processconfig.SandboxLimits {
	t.Helper()
	c, err := processconfig.SandboxCapacity()
	if err != nil {
		t.Fatal(err)
	}
	return c
}
