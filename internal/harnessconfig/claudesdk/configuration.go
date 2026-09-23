// Package claudesdk owns the qualified Claude SDK provider declaration.
package claudesdk

import "github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig"

func Configuration() harnessconfig.Configuration {
	return harnessconfig.Configuration{Providers: []harnessconfig.Provider{{Protocol: "anthropic"}}}
}
