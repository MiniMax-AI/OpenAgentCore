package claudesdk

import "strings"

// withProvider replaces every model credential and endpoint in env with the
// Session's provider.
func withProvider(env, provider []string) []string {
	out := make([]string, 0, len(env)+len(provider))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key != "ANTHROPIC_API_KEY" && key != "ANTHROPIC_AUTH_TOKEN" && key != "ANTHROPIC_BASE_URL" && key != "CLAUDE_CODE_OAUTH_TOKEN" {
			out = append(out, entry)
		}
	}
	return append(out, provider...)
}
