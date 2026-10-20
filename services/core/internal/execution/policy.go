package execution

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
)

// Policy supplies the Harness declarations that admission and dispatch read.
// The zero value uses the built-in Harnesses. Custom composition must supply the
// same policy to the HTTP handler and Dispatcher; Runtime claims never add
// declarations.
type Policy struct {
	Declarations harnessconfig.Registry
}

// Harnesses returns the declarations this policy admits against.
func (p Policy) Harnesses() harnessconfig.Registry {
	if p.Declarations.IsZero() {
		return builtin.Registry()
	}
	return p.Declarations
}
