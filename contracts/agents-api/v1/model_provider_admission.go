package v1

import (
	"net/url"
	"strings"
)

const providerScheme = "https"

// Model provider sources, as recorded in a Session's execution configuration.
const (
	ModelProviderSourceSession    = "session"
	ModelProviderSourceAgent      = "agent"
	ModelProviderSourceDeployment = "deployment"
)

// ModelProviderAllowed reports whether a provider bundle from source may be
// frozen into a Session placed in environment. Caller bundles (Session or saved
// Agent) run on Core-managed or caller-owned compute. The deployment default
// holds the operator's key, so it stays on operator compute: openai_hosted and
// operator-registered none devices. An unknown source is allowed only where
// every source is.
func ModelProviderAllowed(environment, source string) bool {
	caller := source == ModelProviderSourceSession || source == ModelProviderSourceAgent
	deployment := source == ModelProviderSourceDeployment
	switch environment {
	case "openai_hosted":
		return true
	case "self_hosted":
		return caller
	case "none":
		return deployment
	default:
		return false
	}
}

// ModelProviderRequired reports whether a Session in environment cannot run
// without a frozen provider bundle. Hosted and self-hosted Runtimes carry no
// model configuration of their own; a none device may use its own environment.
func ModelProviderRequired(environment string) bool {
	return environment == "openai_hosted" || environment == "self_hosted"
}

func validModelProviderBaseURL(base string) bool {
	u, err := url.Parse(base)
	return err == nil && u.Scheme == providerScheme && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(base, "\x00\r\n")
}
