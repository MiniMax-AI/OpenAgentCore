//go:build linux

// Package netservice is the Linux network service of the Sandbox I/O service.
// It resolves names with the sandbox's resolver and dials TCP from the
// sandbox's network namespace. sandboxnet.Serve runs the Network protocol over
// it: the one Connect, the egress check, the answer and the splice.
package netservice

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"syscall"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxnet"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Service resolves and dials for sandboxnet.Serve.
type Service struct {
	resolver *net.Resolver
	dialer   net.Dialer
}

var _ sandboxnet.Service = (*Service)(nil)

// New returns a service that resolves with the sandbox's system resolver.
func New() *Service { return &Service{resolver: net.DefaultResolver} }

// Handle serves one Network stream under the egress its Bind carries. It is
// the Serve function of the sandboxlink.ServiceNetwork handler.
func (s *Service) Handle(ctx context.Context, b sandboxlink.Bind, st sandboxlink.Stream) {
	sandboxnet.Serve(ctx, st, b.Egress, s)
}

// Resolve returns the IPv4 and IPv6 addresses of host. A name the resolver
// reports as nonexistent, or without addresses, is NameNotResolved.
func (s *Service) Resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	addrs, err := s.resolver.LookupNetIP(ctx, "ip", host)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return nil, &sandboxnet.Error{Code: sandboxnet.CodeNameNotResolved, Effect: sandboxwire.EffectNone, Cause: err}
	}
	return addrs, err
}

// Dial connects to addr. The address is a literal, so nothing is resolved.
func (s *Service) Dial(ctx context.Context, addr netip.AddrPort) (*net.TCPConn, error) {
	c, err := s.dialer.DialContext(ctx, "tcp", addr.String())
	if err != nil {
		return nil, dialError(err)
	}
	return c.(*net.TCPConn), nil
}

// dialError types the errno of a failed connect. Every typed outcome but a
// kernel timeout means no connection was made.
func dialError(err error) error {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return err
	}
	effect := sandboxwire.EffectNone
	var code sandboxnet.Code
	switch errno {
	case syscall.ECONNREFUSED:
		code = sandboxnet.CodeConnectionRefused
	case syscall.ENETUNREACH, syscall.EHOSTUNREACH, syscall.ENETDOWN, syscall.EHOSTDOWN, syscall.EAFNOSUPPORT:
		code = sandboxnet.CodeUnreachable
	case syscall.EADDRNOTAVAIL, syscall.EMFILE, syscall.ENFILE, syscall.ENOBUFS, syscall.ENOMEM, syscall.EAGAIN:
		code = sandboxnet.CodeResourceExhausted
	case syscall.EACCES, syscall.EPERM:
		code = sandboxnet.CodeDenied
	case syscall.ETIMEDOUT:
		code, effect = sandboxnet.CodeTimedOut, sandboxwire.EffectPossible
	default:
		return err
	}
	return &sandboxnet.Error{Code: code, Effect: effect, Cause: err}
}
