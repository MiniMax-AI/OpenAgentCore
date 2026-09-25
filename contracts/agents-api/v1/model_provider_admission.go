package v1

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

const providerEnvironment = "openai_hosted"
const providerScheme = "https"

// providerHost converts a domain as URL host parsing does (UTS #46 without
// hyphen or STD3 restrictions), rejecting invalid labels such as bad punycode.
var providerHost = idna.New(idna.MapForLookup(), idna.BidiRule(), idna.StrictDomainName(false), idna.CheckHyphens(false))

func ModelProviderEnvironmentSupported(environment string) bool {
	return environment == providerEnvironment
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
