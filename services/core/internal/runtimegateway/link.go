package runtimegateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
)

// LinkStore reads the Link authority Core keeps and signs attach grants with
// the credential key.
type LinkStore interface {
	// GetServeAuthority reads the live Link resource with the ID.
	GetServeAuthority(ctx context.Context, id string) (runtimedevice.ServeAuthority, bool, error)
	// GetAgentHostCredential reads a live agent host's credential.
	GetAgentHostCredential(ctx context.Context, runtimeID string) (runtimedevice.AgentHost, bool, error)
	// GetLinkAssignment reads an assignment by its ID.
	GetLinkAssignment(ctx context.Context, assignmentID string) (runtimedevice.LinkAssignment, bool, error)
	// SignAttachGrant returns the keyed hex digest of a grant's payload.
	SignAttachGrant(ctx context.Context, payload string) (string, error)
}

// linkLease bounds how long an attachment outlives a withdrawal that the
// relay did not see.
const linkLease = time.Minute

// LinkAuthority is Core's sandboxlink.Authority. A Serve credential serves
// only its live resource. Only an agent host attaches, and it opens a service
// only with the grant its current bound assignment carries, for the current
// generation of its Session's resource. Every authorization rereads that
// state.
type LinkAuthority struct {
	store LinkStore
}

func NewLinkAuthority(store LinkStore) *LinkAuthority {
	return &LinkAuthority{store: store}
}

var _ sandboxlink.Authority = (*LinkAuthority)(nil)

func (l *LinkAuthority) AuthenticateServe(ctx context.Context, hello sandboxlink.ServeHello) (sandboxlink.ServePeer, error) {
	authority, found, err := l.store.GetServeAuthority(ctx, uuid.UUID(hello.Resource.ID).String())
	if err != nil {
		return sandboxlink.ServePeer{}, err
	}
	digest := sha256.Sum256(hello.Credential)
	if !found || subtle.ConstantTimeCompare([]byte(hex.EncodeToString(digest[:])), []byte(authority.CredentialHash)) != 1 {
		return sandboxlink.ServePeer{}, sandboxlink.Fail(sandboxlink.AuthenticationFailed)
	}
	current := authority.Resource.Ref()
	switch {
	case current.SameResource(hello.Resource) && hello.Resource.Generation < current.Generation:
		return sandboxlink.ServePeer{}, sandboxlink.Fail(sandboxlink.StaleGeneration)
	case current != hello.Resource:
		return sandboxlink.ServePeer{}, sandboxlink.Fail(sandboxlink.PermissionDenied)
	}
	return sandboxlink.ServePeer{PeerID: current.ID, Resource: current}, nil
}

func (l *LinkAuthority) AuthenticateAttach(ctx context.Context, hello sandboxlink.AttachHello) (sandboxlink.AttachPeer, error) {
	host, found, err := l.store.GetAgentHostCredential(ctx, uuid.UUID(hello.RuntimeID).String())
	if err != nil {
		return sandboxlink.AttachPeer{}, err
	}
	presented := runtimedevice.HashCredential(string(hello.Credential))
	if !found || subtle.ConstantTimeCompare([]byte(presented), []byte(host.CredentialHash)) != 1 {
		return sandboxlink.AttachPeer{}, sandboxlink.Fail(sandboxlink.AuthenticationFailed)
	}
	return sandboxlink.AttachPeer{RuntimeID: hello.RuntimeID, Revision: host.Revision}, nil
}

// AuthorizeOpen grants File the world export and Network every address
// while the Environment's network access is enabled, and nothing otherwise.
func (l *LinkAuthority) AuthorizeOpen(ctx context.Context, peer sandboxlink.AttachPeer, open sandboxlink.Open) (sandboxlink.Authorization, error) {
	identity, assignment, err := l.authorize(ctx, peer, open.AttachGrant)
	if err != nil {
		return sandboxlink.Authorization{}, err
	}
	identity.AttachmentID = open.AttachmentID
	if identity != open.Identity() {
		return sandboxlink.Authorization{}, sandboxlink.Fail(sandboxlink.PermissionDenied)
	}
	auth := sandboxlink.Authorization{Identity: identity, Service: open.Service, LeaseExpiresAt: time.Now().Add(linkLease)}
	switch {
	case open.Service == sandboxlink.ServiceFile:
		auth.Exports = []sandboxlink.ExportGrant{{ID: sandboxfs.WorldExport}}
	case open.Service == sandboxlink.ServiceNetwork && assignment.NetworkEnabled:
		auth.Egress = allEgress
	}
	return auth, nil
}

func (l *LinkAuthority) Renew(ctx context.Context, peer sandboxlink.AttachPeer, renew sandboxlink.RenewAttachment) (sandboxlink.Authorization, error) {
	identity, _, err := l.authorize(ctx, peer, renew.AttachGrant)
	if err != nil {
		return sandboxlink.Authorization{}, err
	}
	identity.AttachmentID = renew.AttachmentID
	return sandboxlink.Authorization{Identity: identity, LeaseExpiresAt: time.Now().Add(linkLease)}, nil
}

var allEgress = []sandboxlink.EgressRule{
	{Prefix: netip.MustParsePrefix("0.0.0.0/0"), PortFirst: 1, PortLast: 65535},
	{Prefix: netip.MustParsePrefix("::/0"), PortFirst: 1, PortLast: 65535},
}

// authorize returns the attachment identity, without its attachment ID, that
// grant authorizes for peer: the grant's assignment must be the current bound
// assignment of peer's Runtime, and its resource generation the current one.
func (l *LinkAuthority) authorize(ctx context.Context, peer sandboxlink.AttachPeer, grant []byte) (sandboxlink.Identity, runtimedevice.LinkAssignment, error) {
	if len(grant) != grantBytes {
		return sandboxlink.Identity{}, runtimedevice.LinkAssignment{}, sandboxlink.Fail(sandboxlink.PermissionDenied)
	}
	id := sandboxwire.ID(grant[:16])
	epoch, generation := binary.BigEndian.Uint64(grant[16:24]), binary.BigEndian.Uint64(grant[24:32])
	assignment, found, err := l.store.GetLinkAssignment(ctx, uuid.UUID(id).String())
	switch {
	case err != nil:
		return sandboxlink.Identity{}, assignment, err
	case !found || assignment.RuntimeID != uuid.UUID(peer.RuntimeID).String():
		return sandboxlink.Identity{}, assignment, sandboxlink.Fail(sandboxlink.PermissionDenied)
	case !assignment.AgentHost || assignment.Revision != peer.Revision:
		return sandboxlink.Identity{}, assignment, sandboxlink.Fail(sandboxlink.AuthenticationFailed)
	case assignment.Resource.Kind == "":
		return sandboxlink.Identity{}, assignment, sandboxlink.Fail(sandboxlink.ResourceNotFound)
	}
	want, err := l.grant(ctx, id, epoch, assignment.Resource.Kind, assignment.Resource.ID, generation)
	if err != nil {
		return sandboxlink.Identity{}, assignment, err
	}
	resource := assignment.Resource.Ref()
	switch {
	case !hmac.Equal(want, grant):
		return sandboxlink.Identity{}, assignment, sandboxlink.Fail(sandboxlink.PermissionDenied)
	case epoch != assignment.Epoch || !assignment.Bound:
		return sandboxlink.Identity{}, assignment, sandboxlink.Fail(sandboxlink.StaleAssignment)
	case generation < resource.Generation:
		return sandboxlink.Identity{}, assignment, sandboxlink.Fail(sandboxlink.StaleGeneration)
	case generation != resource.Generation:
		return sandboxlink.Identity{}, assignment, sandboxlink.Fail(sandboxlink.PermissionDenied)
	}
	session, err := uuid.Parse(assignment.SessionID)
	if err != nil {
		return sandboxlink.Identity{}, assignment, err
	}
	return sandboxlink.Identity{Resource: resource, SessionID: sandboxwire.ID(session), AssignmentID: id, AssignmentEpoch: epoch}, assignment, nil
}

// bindLink returns the Link resource and attach grant that runtimeID's bind of
// ref in environmentID carries: none unless runtimeID is the agent host that
// holds ref bound and environmentID has a live Link resource.
func (l *LinkAuthority) bindLink(ctx context.Context, runtimeID string, ref proto.AssignmentRef, environmentID string) (*sandboxbootstrap.Resource, []byte, error) {
	if environmentID == "" {
		return nil, nil, nil
	}
	assignment, found, err := l.store.GetLinkAssignment(ctx, ref.AssignmentID)
	if err != nil || !found || !assignment.AgentHost || !assignment.Bound || assignment.Resource.EnvironmentID != environmentID ||
		assignment.RuntimeID != runtimeID || assignment.SessionID != ref.SessionID || assignment.Epoch != ref.Epoch {
		return nil, nil, err
	}
	id, err := uuid.Parse(ref.AssignmentID)
	if err != nil {
		return nil, nil, err
	}
	resource := assignment.Resource
	grant, err := l.grant(ctx, sandboxwire.ID(id), ref.Epoch, resource.Kind, resource.ID, resource.Generation)
	if err != nil {
		return nil, nil, err
	}
	return &resource, grant, nil
}

// grantBytes is the size of an attach grant: the assignment ID, epoch and
// resource generation, then the hex digest that binds them to the resource.
const grantBytes = 16 + 8 + 8 + 2*sha256.Size

// grant returns the attach grant of an assignment epoch on a resource
// generation.
func (l *LinkAuthority) grant(ctx context.Context, assignment sandboxwire.ID, epoch uint64, kind, resource string, generation uint64) ([]byte, error) {
	header := make([]byte, 0, grantBytes)
	header = append(header, assignment[:]...)
	header = binary.BigEndian.AppendUint64(header, epoch)
	header = binary.BigEndian.AppendUint64(header, generation)
	digest, err := l.store.SignAttachGrant(ctx, fmt.Sprintf("%x/%s/%s", header, kind, resource))
	if err != nil {
		return nil, err
	}
	if len(digest) != 2*sha256.Size {
		return nil, fmt.Errorf("attach grant digest of %d bytes", len(digest))
	}
	return append(header, digest...), nil
}
