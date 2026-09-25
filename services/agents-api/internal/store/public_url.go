package store

import (
	"context"
	"errors"
)

// ErrSandboxPublicURLUnreachable rejects E2B selections and new E2B Sessions
// while the installation public URL is loopback: E2B sandboxes reach Core from
// E2B's cloud.
var ErrSandboxPublicURLUnreachable = errors.New("E2B sandboxes reach Core over the internet. Set an HTTPS public URL that is not loopback (public_url in config.json, AGENTS_API_PUBLIC_URL for Core).")

// SetPublicURL records AGENTS_API_PUBLIC_URL, validated by the caller. Core
// reports it as the deployment and node configuration core_url and records it
// on each node it enrolls. Call it once, before serving requests.
func (s *Store) SetPublicURL(value string) { s.publicURL = value }

// AddressBindings counts what is bound to an installation address: nodes
// connect to the address they enrolled with, hosted sandboxes were started with
// the address current at the time, and self-hosted executors were installed
// with an advertised remote_url.
type AddressBindings struct {
	Nodes               int64 `json:"nodes"`
	NodesOnOtherAddress int64 `json:"nodes_on_other_address"`
	HostedSandboxes     int64 `json:"hosted_sandboxes"`
	SelfHostedExecutors int64 `json:"self_hosted_executors"`
}

func (s *Store) AddressBindings(ctx context.Context) (AddressBindings, error) {
	bindings, err := s.queries.CountAddressBindings(ctx, s.publicURL)
	if err != nil {
		return AddressBindings{}, err
	}
	resources, err := s.queries.CountRuntimeDeploymentResources(ctx)
	if err != nil {
		return AddressBindings{}, err
	}
	return AddressBindings{Nodes: bindings.Nodes, NodesOnOtherAddress: bindings.NodesOnOtherAddress,
		HostedSandboxes: resources.Allocations + resources.Pending, SelfHostedExecutors: bindings.SelfHostedExecutors}, nil
}
