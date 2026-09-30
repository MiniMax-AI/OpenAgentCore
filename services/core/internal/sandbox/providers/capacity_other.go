//go:build !linux

package providers

import (
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func hostCapacity(sandbox.Resources) error { return errors.New("sandbox nodes require Linux") }
