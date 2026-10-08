//go:build !linux

package microsandbox

import (
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func hostCapacity(sandbox.Resources) error { return errors.New("sandbox nodes require Linux") }
