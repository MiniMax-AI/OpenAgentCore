package claudesdk

import (
	"strings"

	harnessconfiguration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/claudesdk"
)

// Validate the frozen native provider before producing launch variables.
func providerEnvironment(value any) ([]string, error) {
	provider, err := harnessconfiguration.Configuration().ParseProvider(value)
	if err != nil {
		return nil, err
	}
	return []string{"ANTHROPIC_BASE_URL=" + provider.BaseURL, "ANTHROPIC_AUTH_TOKEN=" + provider.APIKey}, nil
}

func withProvider(env, provider []string) []string {
	if provider == nil {
		return env
	}
	out := make([]string, 0, len(env)+len(provider))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key != "ANTHROPIC_API_KEY" && key != "ANTHROPIC_AUTH_TOKEN" && key != "ANTHROPIC_BASE_URL" {
			out = append(out, entry)
		}
	}
	return append(out, provider...)
}
