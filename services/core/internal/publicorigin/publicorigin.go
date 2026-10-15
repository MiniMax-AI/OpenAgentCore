// Package publicorigin owns OAC_PUBLIC_URL's one rule, shared by Core and the
// installer.
package publicorigin

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ErrInvalid reports a value that is not a canonical public origin.
var ErrInvalid = errors.New("invalid public origin")

// Origin is OAC_PUBLIC_URL, the one origin applications, nodes,
// sandboxes and self-hosted executors use. Every Core address they are given
// is derived from it; none is parsed back into an origin.
type Origin struct{ origin string }

// New accepts a canonical public origin, never a path or
// credential. It may be http or https: a reverse proxy in front of Web
// terminates TLS when the installation uses it.
func New(value string) (Origin, error) {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" || u.String() != value || u.Host != strings.ToLower(u.Host) {
		return Origin{}, ErrInvalid
	}
	if strings.ContainsAny(u.Host, "\\% \t\r\n") || strings.HasSuffix(u.Host, ":") {
		return Origin{}, ErrInvalid
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return Origin{}, ErrInvalid
		}
	}
	if net.ParseIP(u.Hostname()) == nil {
		if len(u.Hostname()) > 253 || strings.ContainsAny(u.Host, "[]") {
			return Origin{}, ErrInvalid
		}
		for _, label := range strings.Split(u.Hostname(), ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return Origin{}, ErrInvalid
			}
			for _, char := range label {
				if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
					return Origin{}, ErrInvalid
				}
			}
		}
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return Origin{}, ErrInvalid
	}
	return Origin{origin: value}, nil
}

// String is the origin itself.
func (o Origin) String() string { return o.origin }

// API is the base URL of the Agents API.
func (o Origin) API() string { return o.origin + "/v1" }

// RuntimeAPI is the base URL sandboxes and nodes call.
func (o Origin) RuntimeAPI() string { return o.origin + "/api/v1" }

// DaemonWebSocket is the daemon transport: ws on an http origin, wss on https.
func (o Origin) DaemonWebSocket() string {
	return "ws" + strings.TrimPrefix(o.origin, "http") + "/api/v1/agent-daemon/ws"
}

// InstallerBase is the prefix of the versioned native installer downloads.
func (o Origin) InstallerBase() string { return o.origin + "/api/v1/agent-daemon/install/" }
