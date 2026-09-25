package v1

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

const providerScheme = "https"

// providerHost converts a domain as URL host parsing does (UTS #46 without
// hyphen or STD3 restrictions), rejecting invalid labels such as bad punycode.
var providerHost = idna.New(idna.MapForLookup(), idna.BidiRule(), idna.StrictDomainName(false), idna.CheckHyphens(false))

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
	return err == nil && u.Scheme == providerScheme && validModelProviderHost(u) && u.User == nil && u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(base, "\x00\r\n")
}

// validModelProviderHost requires a usable host: an IP address, or a domain
// whose labels are nonempty letters, digits, hyphens and underscores and whose
// final label is not numeric. Any port must be in 1-65535.
func validModelProviderHost(u *url.URL) bool {
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		return true
	}
	ascii, err := providerHost.ToASCII(host)
	if err != nil {
		return false
	}
	labels := strings.Split(strings.TrimSuffix(ascii, "."), ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label == "xn--" || strings.IndexFunc(label, invalidHostRune) >= 0 {
			return false
		}
	}
	// A numeric final label makes the host an IPv4 address, which ParseIP rejected.
	return strings.Trim(labels[len(labels)-1], "0123456789") != ""
}

func invalidHostRune(r rune) bool {
	return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_')
}
