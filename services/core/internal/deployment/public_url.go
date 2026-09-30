package deployment

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ValidateCoreURL accepts a canonical public origin, never a path or
// credential. Plain HTTP is reserved for explicit loopback development hosts.
// OAC_PUBLIC_URL must pass it.
func ValidateCoreURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" || u.String() != value || u.Host != strings.ToLower(u.Host) {
		return ErrInvalidInput
	}
	if strings.ContainsAny(u.Host, "\\% \t\r\n") || strings.HasSuffix(u.Host, ":") {
		return ErrInvalidInput
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return ErrInvalidInput
		}
	}
	loopback := u.Hostname() == "localhost"
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	} else {
		if len(u.Hostname()) > 253 || strings.ContainsAny(u.Host, "[]") {
			return ErrInvalidInput
		}
		for _, label := range strings.Split(u.Hostname(), ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return ErrInvalidInput
			}
			for _, char := range label {
				if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
					return ErrInvalidInput
				}
			}
		}
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return ErrInvalidInput
	}
	return nil
}

// LoopbackOrigin reports whether a validated origin names a loopback host, which
// nothing outside the Core host can reach.
func LoopbackOrigin(value string) bool {
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		return ip.IsLoopback()
	}
	return u.Hostname() == "localhost"
}
