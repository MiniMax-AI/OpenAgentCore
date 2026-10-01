// Package sandboxlinktest provides an in-process Link Authority with static
// credentials and grants, and a relay on an httptest TLS server, for tests of
// Link peers and of the services behind them.
package sandboxlinktest

import (
	"context"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// AllowAll is the egress the fixture grants by default: every IPv4 and IPv6
// address on every port.
var AllowAll = []sandboxlink.EgressRule{
	{Prefix: netip.MustParsePrefix("0.0.0.0/0"), PortFirst: 1, PortLast: 65535},
	{Prefix: netip.MustParsePrefix("::/0"), PortFirst: 1, PortLast: 65535},
}

// DefaultExports is the export set the fixture grants to file streams by
// default: the export "world", read-write.
var DefaultExports = []sandboxlink.ExportGrant{{ID: "world"}}

// Grant is what one attachment grant authorizes. A nil Exports grants
// DefaultExports to file streams. A nil Egress grants AllowAll to network
// streams; an empty non-nil Egress denies all.
type Grant struct {
	RuntimeID       sandboxwire.ID
	Resource        sandboxlink.ResourceRef
	SessionID       sandboxwire.ID
	AssignmentID    sandboxwire.ID
	AssignmentEpoch uint64
	Services        []sandboxlink.Service
	Lease           time.Duration
	Exports         []sandboxlink.ExportGrant
	Egress          []sandboxlink.EgressRule
}

// Authority is a sandboxlink.Authority over static tables. It is safe for
// concurrent use; changes apply to later calls.
type Authority struct {
	mu       sync.Mutex
	serves   map[string]sandboxlink.ServePeer
	runtimes map[string]sandboxwire.ID
	grants   map[string]Grant
}

// NewAuthority returns an empty Authority.
func NewAuthority() *Authority {
	return &Authority{serves: map[string]sandboxlink.ServePeer{}, runtimes: map[string]sandboxwire.ID{}, grants: map[string]Grant{}}
}

// AddServe accepts credential for a serve peer.
func (a *Authority) AddServe(credential []byte, peer sandboxlink.ServePeer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.serves[string(credential)] = peer
}

// RemoveServe withdraws a serve credential.
func (a *Authority) RemoveServe(credential []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.serves, string(credential))
}

// AddRuntime accepts credential for a Runtime.
func (a *Authority) AddRuntime(credential []byte, runtimeID sandboxwire.ID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.runtimes[string(credential)] = runtimeID
}

// AddGrant accepts an attachment grant.
func (a *Authority) AddGrant(grant []byte, g Grant) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.grants[string(grant)] = g
}

// RemoveGrant withdraws an attachment grant.
func (a *Authority) RemoveGrant(grant []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.grants, string(grant))
}

func (a *Authority) AuthenticateServe(_ context.Context, hello sandboxlink.ServeHello) (sandboxlink.ServePeer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	peer, ok := a.serves[string(hello.Credential)]
	if !ok {
		return sandboxlink.ServePeer{}, sandboxlink.Fail(sandboxlink.AuthenticationFailed)
	}
	return peer, nil
}

func (a *Authority) AuthenticateAttach(_ context.Context, hello sandboxlink.AttachHello) (sandboxlink.AttachPeer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, ok := a.runtimes[string(hello.Credential)]
	if !ok {
		return sandboxlink.AttachPeer{}, sandboxlink.Fail(sandboxlink.AuthenticationFailed)
	}
	return sandboxlink.AttachPeer{RuntimeID: id, Revision: 1}, nil
}

// AuthorizeOpen admits an Open that matches its grant exactly. An older
// resource generation is StaleGeneration and an older assignment epoch is
// StaleAssignment; a resource no serve credential serves is ResourceNotFound.
func (a *Authority) AuthorizeOpen(_ context.Context, peer sandboxlink.AttachPeer, open sandboxlink.Open) (sandboxlink.Authorization, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	g, ok := a.grants[string(open.AttachGrant)]
	if !ok || g.RuntimeID != peer.RuntimeID || !g.Resource.SameResource(open.Resource) ||
		g.SessionID != open.SessionID || g.AssignmentID != open.AssignmentID || !slices.Contains(g.Services, open.Service) {
		return sandboxlink.Authorization{}, sandboxlink.Fail(sandboxlink.PermissionDenied)
	}
	switch {
	case open.Resource.Generation < g.Resource.Generation:
		return sandboxlink.Authorization{}, sandboxlink.Fail(sandboxlink.StaleGeneration)
	case open.AssignmentEpoch < g.AssignmentEpoch:
		return sandboxlink.Authorization{}, sandboxlink.Fail(sandboxlink.StaleAssignment)
	case open.Resource != g.Resource || open.AssignmentEpoch != g.AssignmentEpoch:
		return sandboxlink.Authorization{}, sandboxlink.Fail(sandboxlink.PermissionDenied)
	}
	served := false
	for _, p := range a.serves {
		served = served || p.Resource == open.Resource
	}
	if !served {
		return sandboxlink.Authorization{}, sandboxlink.Fail(sandboxlink.ResourceNotFound)
	}
	auth := sandboxlink.Authorization{Identity: open.Identity(), Service: open.Service, LeaseExpiresAt: time.Now().Add(g.Lease)}
	switch open.Service {
	case sandboxlink.ServiceFile:
		auth.Exports = g.Exports
		if auth.Exports == nil {
			auth.Exports = DefaultExports
		}
	case sandboxlink.ServiceNetwork:
		auth.Egress = g.Egress
		if auth.Egress == nil {
			auth.Egress = AllowAll
		}
	}
	return auth, nil
}

// Renew extends a lease by the grant's Lease.
func (a *Authority) Renew(_ context.Context, peer sandboxlink.AttachPeer, renew sandboxlink.RenewAttachment) (sandboxlink.Authorization, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	g, ok := a.grants[string(renew.AttachGrant)]
	if !ok || g.RuntimeID != peer.RuntimeID {
		return sandboxlink.Authorization{}, sandboxlink.Fail(sandboxlink.PermissionDenied)
	}
	return sandboxlink.Authorization{
		Identity: sandboxlink.Identity{AttachmentID: renew.AttachmentID, Resource: g.Resource, SessionID: g.SessionID,
			AssignmentID: g.AssignmentID, AssignmentEpoch: g.AssignmentEpoch},
		LeaseExpiresAt: time.Now().Add(g.Lease),
	}, nil
}
