package deployment

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// PublicOrigin is OAC_PUBLIC_URL, the one origin applications, nodes,
// sandboxes and self-hosted executors use. Every Core address they are given
// is derived from it; none is parsed back into an origin.
type PublicOrigin struct{ origin string }

// NewPublicOrigin accepts a canonical public origin, never a path or
// credential. It may be http or https: a reverse proxy in front of Web
// terminates TLS when the installation uses it.
func NewPublicOrigin(value string) (PublicOrigin, error) {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" || u.String() != value || u.Host != strings.ToLower(u.Host) {
		return PublicOrigin{}, ErrInvalidInput
	}
	if strings.ContainsAny(u.Host, "\\% \t\r\n") || strings.HasSuffix(u.Host, ":") {
		return PublicOrigin{}, ErrInvalidInput
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return PublicOrigin{}, ErrInvalidInput
		}
	}
	if net.ParseIP(u.Hostname()) == nil {
		if len(u.Hostname()) > 253 || strings.ContainsAny(u.Host, "[]") {
			return PublicOrigin{}, ErrInvalidInput
		}
		for _, label := range strings.Split(u.Hostname(), ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return PublicOrigin{}, ErrInvalidInput
			}
			for _, char := range label {
				if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
					return PublicOrigin{}, ErrInvalidInput
				}
			}
		}
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return PublicOrigin{}, ErrInvalidInput
	}
	return PublicOrigin{origin: value}, nil
}

// String is the origin itself.
func (o PublicOrigin) String() string { return o.origin }

// API is the base URL of the Agents API.
func (o PublicOrigin) API() string { return o.origin + "/v1" }

// RuntimeAPI is the base URL sandboxes and nodes call.
func (o PublicOrigin) RuntimeAPI() string { return o.origin + "/api/v1" }

// DaemonWebSocket is the daemon transport: ws on an http origin, wss on https.
func (o PublicOrigin) DaemonWebSocket() string {
	return "ws" + strings.TrimPrefix(o.origin, "http") + "/api/v1/agent-daemon/ws"
}

// InstallerBase is the prefix of the versioned native installer downloads.
func (o PublicOrigin) InstallerBase() string { return o.origin + "/api/v1/agent-daemon/install/" }

// AddressBindings counts what is bound to an installation address: nodes
// connect to the address they enrolled with, hosted sandboxes were started with
// the address current at the time, and self-hosted executors were installed
// with an advertised remote_url.
type AddressBindings struct {
	Nodes               int64 `json:"nodes" binding:"required"`
	NodesOnOtherAddress int64 `json:"nodes_on_other_address" binding:"required"`
	HostedSandboxes     int64 `json:"hosted_sandboxes" binding:"required"`
	SelfHostedExecutors int64 `json:"self_hosted_executors" binding:"required"`
}

// AddressBindings counts what is bound to the installation public URL.
func (s *Service) AddressBindings(ctx context.Context) (AddressBindings, error) {
	return s.reader.AddressBindings(ctx, s.rules.PublicURL())
}
