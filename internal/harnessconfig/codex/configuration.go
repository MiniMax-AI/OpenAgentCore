// Package codex owns the qualified Codex provider configuration declaration.
package codex

import "github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig"

func Configuration() harnessconfig.Configuration {
	return harnessconfig.Configuration{Providers: []harnessconfig.Provider{{Protocol: "responses"}}}
}
