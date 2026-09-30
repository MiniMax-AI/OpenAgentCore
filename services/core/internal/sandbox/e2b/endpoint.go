package e2b

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

const OfficialAPIURL = "https://api.e2b.app"
const OfficialDomain = "e2b.app"

var ErrEndpoint = errors.New("invalid E2B endpoint")

// NormalizeEndpoint returns the two SDK routing selectors. Both must be set
// together for a compatible service; omission selects the official cloud.
func NormalizeEndpoint(apiURL, domain string) (string, string, error) {
	if apiURL == "" && domain == "" {
		return OfficialAPIURL, OfficialDomain, nil
	}
	if apiURL == "" || domain == "" || len(apiURL) > 512 || len(domain) > 253 || !validDomain(domain) {
		return "", "", ErrEndpoint
	}
	u, err := url.Parse(apiURL)
	if err != nil || u.Scheme != "https" || u.Host != u.Hostname() || u.Host == "" || u.User != nil ||
		u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		u.RawFragment != "" || u.Opaque != "" || u.String() != apiURL || !validDomain(u.Hostname()) {
		return "", "", ErrEndpoint
	}
	if u.Hostname() != domain && !strings.HasSuffix(u.Hostname(), "."+domain) {
		return "", "", ErrEndpoint
	}
	return apiURL, domain, nil
}

func validDomain(host string) bool {
	if len(host) > 253 || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		net.ParseIP(host) != nil || host != strings.ToLower(host) || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
