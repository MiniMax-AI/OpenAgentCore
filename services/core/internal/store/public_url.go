package store

import (
	"context"
)

// SetPublicURL records OAC_PUBLIC_URL, validated by the caller. Placement
// admits only nodes enrolled with it. Call it once, before serving requests.
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
